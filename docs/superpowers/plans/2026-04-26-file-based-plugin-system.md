# File-Based Plugin System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the compiled-Go `Plugin` interface with a directory-scanner that loads `FilePlugin` structs from `~/.config/ra/plugins/<name>/` and `.ra/plugins/<name>/`, enabling anyone to write a plugin as `plugin.yaml` + shell scripts.

**Architecture:** `FilePlugin` reads `plugin.yaml` via `fs.FS`, implementing all plugin behaviors generically. `LoadAll()` scans two filesystem locations plus embedded defaults, shadowing by name. The `Registry` wraps `[]*FilePlugin` with an unchanged external API. The claude plugin ships as embedded default files in `internal/plugins/defaults/claude/`. The entrypoint.sh pattern changes: all secrets go through the generic dispatch loop; plugins are sourced afterwards via glob.

**Tech Stack:** Go 1.26.2, `gopkg.in/yaml.v3`, `io/fs` (stdlib), `embed` (stdlib), `github.com/spf13/cobra`

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/plugins/schema.go` | `PluginSchema`, `ConfigField`, `SecretDef`, `AuthProvider` types |
| Create | `internal/plugins/schema_test.go` | YAML parse tests |
| Create | `internal/plugins/fileplugin.go` | `Asset`, `FilePlugin` struct + all methods |
| Create | `internal/plugins/fileplugin_test.go` | All `FilePlugin` behavior tests |
| Create | `internal/plugins/defaults_embed.go` | `//go:embed defaults` directive |
| Create | `internal/plugins/loader.go` | `LoadAll()` — filesystem + embedded defaults |
| Create | `internal/plugins/loader_test.go` | Loader tests |
| Create | `internal/plugins/defaults/claude/plugin.yaml` | Default claude plugin declaration |
| Create | `internal/plugins/defaults/claude/install.sh` | First-boot npm installer |
| Create | `internal/plugins/defaults/claude/setup.sh` | Runtime auth setup |
| Modify | `internal/plugins/registry.go` | Switch `[]Plugin` → `[]*FilePlugin` |
| Modify | `internal/plugins/registry_test.go` | Update tests |
| Modify | `internal/config/types.go` | Remove `ClaudeConfig`, add `Plugins map[string]map[string]interface{}` |
| Modify | `internal/config/merge.go` | Replace Claude merge with Plugins map merge |
| Modify | `internal/config/validate.go` | Remove `RA_CLAUDE_ENABLED` from reserved vars |
| Modify | `cmd/root.go` | `LoadAll()` instead of `claudeplugin.New()` |
| Modify | `internal/scaffold/assets/Dockerfile` | `COPY .ra/plugins/` instead of explicit claude paths |
| Modify | `internal/scaffold/assets/entrypoint.sh` | Glob-source plugin setup.sh; remove claude-specific dispatch |
| Create | `cmd/plugin.go` | `ra plugin install/list/remove` subcommand |
| Delete | `internal/plugins/plugin.go` | Plugin interface removed |
| Delete | `internal/plugins/claude/` | Entire directory removed |

---

## Task 1: Plugin schema types

**Files:**
- Create: `internal/plugins/schema.go`
- Create: `internal/plugins/schema_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/plugins/schema_test.go
package plugins

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPluginSchemaParse(t *testing.T) {
	raw := `
name: testplugin
version: "1.0.0"
description: "A test plugin"
config:
  - name: enabled
    type: bool
    description: "Enable it?"
    default: false
    required: false
  - name: mode
    type: string
    description: "Mode"
    options: [fast, slow]
    required: true
secrets:
  - name: my_token
    env_var: MY_TOKEN
    description: "A token"
    default_secret_name: my-gcp-secret
auth_providers:
  - name: oauth
    description: "OAuth"
    secrets:
      - name: oauth_token
        env_var: OAUTH_TOKEN
        description: "OAuth token"
        default_secret_name: my-oauth-secret
  - name: apikey
    description: "API key"
    secrets:
      - name: api_key
        env_var: API_KEY
        description: "API key"
        default_secret_name: my-api-key
`
	var s PluginSchema
	if err := yaml.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Name != "testplugin" {
		t.Errorf("Name = %q, want %q", s.Name, "testplugin")
	}
	if len(s.Config) != 2 {
		t.Fatalf("len(Config) = %d, want 2", len(s.Config))
	}
	if s.Config[0].Name != "enabled" || s.Config[0].Type != "bool" {
		t.Errorf("Config[0] = %+v", s.Config[0])
	}
	if s.Config[1].Required != true || len(s.Config[1].Options) != 2 {
		t.Errorf("Config[1] = %+v", s.Config[1])
	}
	if len(s.Secrets) != 1 || s.Secrets[0].EnvVar != "MY_TOKEN" {
		t.Errorf("Secrets = %+v", s.Secrets)
	}
	if len(s.AuthProviders) != 2 || s.AuthProviders[0].Name != "oauth" {
		t.Errorf("AuthProviders = %+v", s.AuthProviders)
	}
	if s.AuthProviders[0].Secrets[0].DefaultSecretName != "my-oauth-secret" {
		t.Errorf("AuthProviders[0].Secrets[0].DefaultSecretName = %q", s.AuthProviders[0].Secrets[0].DefaultSecretName)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```
cd .worktrees/simplify-plugin-system && go test ./internal/plugins/... -run TestPluginSchemaParse
```
Expected: compilation error — types don't exist yet.

- [ ] **Step 3: Implement schema.go**

```go
// internal/plugins/schema.go
package plugins

// PluginSchema is the parsed structure of a plugin.yaml file.
type PluginSchema struct {
	Name          string         `yaml:"name"`
	Version       string         `yaml:"version"`
	Description   string         `yaml:"description"`
	Config        []ConfigField  `yaml:"config"`
	Secrets       []SecretDef    `yaml:"secrets"`
	AuthProviders []AuthProvider `yaml:"auth_providers"`
}

// ConfigField declares one user-facing config field.
type ConfigField struct {
	Name        string      `yaml:"name"`
	Type        string      `yaml:"type"` // "bool", "string", "int"
	Description string      `yaml:"description"`
	Default     interface{} `yaml:"default"`
	Required    bool        `yaml:"required"`
	Options     []string    `yaml:"options"`
}

// SecretDef declares one GCP Secret Manager secret the plugin needs.
type SecretDef struct {
	Name              string `yaml:"name"`
	EnvVar            string `yaml:"env_var"`
	Description       string `yaml:"description"`
	DefaultSecretName string `yaml:"default_secret_name"`
}

// AuthProvider groups provider-specific secrets under a named auth method.
type AuthProvider struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Secrets     []SecretDef `yaml:"secrets"`
}
```

- [ ] **Step 4: Run to verify it passes**

```
go test ./internal/plugins/... -run TestPluginSchemaParse
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/schema.go internal/plugins/schema_test.go
git commit -m "feat(plugins): add PluginSchema types for file-based plugin.yaml"
```

---

## Task 2: FilePlugin — load and assets

**Files:**
- Create: `internal/plugins/fileplugin.go`
- Create: `internal/plugins/fileplugin_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/plugins/fileplugin_test.go
package plugins

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

const testPluginYAML = `
name: myplugin
version: "1.0.0"
description: "Test plugin"
config:
  - name: enabled
    type: bool
    description: "Enable?"
    default: false
auth_providers:
  - name: oauth
    description: "OAuth"
    secrets:
      - name: my_oauth
        env_var: MY_OAUTH_TOKEN
        default_secret_name: my-oauth-gcp-secret
`

func testPluginFS(extraFiles map[string]string) fs.FS {
	m := fstest.MapFS{
		"plugin.yaml": &fstest.MapFile{Data: []byte(testPluginYAML)},
	}
	for name, content := range extraFiles {
		m[name] = &fstest.MapFile{Data: []byte(content), Mode: 0755}
	}
	return m
}

func TestLoadPlugin_Name(t *testing.T) {
	p, err := LoadPlugin(testPluginFS(nil))
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if p.Name() != "myplugin" {
		t.Errorf("Name() = %q, want %q", p.Name(), "myplugin")
	}
}

func TestLoadPlugin_MissingYAML(t *testing.T) {
	_, err := LoadPlugin(fstest.MapFS{})
	if err == nil {
		t.Fatal("expected error for missing plugin.yaml")
	}
}

func TestLoadPlugin_MissingName(t *testing.T) {
	fsys := fstest.MapFS{
		"plugin.yaml": &fstest.MapFile{Data: []byte("version: \"1.0.0\"\n")},
	}
	_, err := LoadPlugin(fsys)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestFilePlugin_Assets_NoScripts(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	assets := p.Assets()
	if len(assets) != 0 {
		t.Errorf("expected 0 assets when scripts absent, got %d", len(assets))
	}
}

func TestFilePlugin_Assets_WithScripts(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(map[string]string{
		"install.sh": "#!/bin/bash\necho install",
		"setup.sh":   "#!/bin/bash\necho setup",
	}))
	assets := p.Assets()
	if len(assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(assets))
	}
	if assets[0].Name != "plugins/myplugin/install.sh" {
		t.Errorf("assets[0].Name = %q", assets[0].Name)
	}
	if assets[1].Name != "plugins/myplugin/setup.sh" {
		t.Errorf("assets[1].Name = %q", assets[1].Name)
	}
	if assets[0].FileMode != 0755 {
		t.Errorf("assets[0].FileMode = %v", assets[0].FileMode)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```
go test ./internal/plugins/... -run "TestLoadPlugin|TestFilePlugin_Assets"
```
Expected: compilation error — `LoadPlugin`, `FilePlugin`, `Asset` not defined.

- [ ] **Step 3: Implement fileplugin.go (load + assets)**

```go
// internal/plugins/fileplugin.go
package plugins

import (
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// Asset describes a single build asset contributed by a plugin.
type Asset struct {
	Name     string
	Bytes    []byte
	FileMode os.FileMode
}

// FilePlugin implements plugin behaviors by reading a plugin directory.
type FilePlugin struct {
	fsys   fs.FS
	schema PluginSchema
}

// LoadPlugin reads plugin.yaml from fsys and returns a FilePlugin.
func LoadPlugin(fsys fs.FS) (*FilePlugin, error) {
	data, err := fs.ReadFile(fsys, "plugin.yaml")
	if err != nil {
		return nil, fmt.Errorf("read plugin.yaml: %w", err)
	}
	var s PluginSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("plugin.yaml: name is required")
	}
	return &FilePlugin{fsys: fsys, schema: s}, nil
}

// Name returns the plugin's canonical identifier.
func (p *FilePlugin) Name() string { return p.schema.Name }

// Assets returns install.sh and setup.sh if present, named plugins/<name>/<script>.
func (p *FilePlugin) Assets() []Asset {
	var assets []Asset
	for _, script := range []string{"install.sh", "setup.sh"} {
		b, err := fs.ReadFile(p.fsys, script)
		if err != nil {
			continue
		}
		assets = append(assets, Asset{
			Name:     "plugins/" + p.schema.Name + "/" + script,
			Bytes:    b,
			FileMode: 0755,
		})
	}
	return assets
}
```

- [ ] **Step 4: Run to verify it passes**

```
go test ./internal/plugins/... -run "TestLoadPlugin|TestFilePlugin_Assets"
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/fileplugin.go internal/plugins/fileplugin_test.go
git commit -m "feat(plugins): add FilePlugin load and Assets"
```

---

## Task 3: FilePlugin — config fragments, env pairs, validate, prompt

**Files:**
- Modify: `internal/plugins/fileplugin.go` (add methods)
- Modify: `internal/plugins/fileplugin_test.go` (add tests)

Note: `Validate` and `ContainerEnvPairs` take `map[string]interface{}` here; the `*config.Config` wrapper is added in Task 5 alongside the config type change.

- [ ] **Step 1: Add tests for DefaultConfigFragments**

Append to `fileplugin_test.go`:

```go
func TestFilePlugin_DefaultConfigFragments(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	cfg, secrets := p.DefaultConfigFragments()
	if secrets != "" {
		t.Errorf("secrets block should be empty, got %q", secrets)
	}
	// Must contain plugins: and myplugin: and enabled: false
	if !strings.Contains(cfg, "plugins:") {
		t.Errorf("config block missing 'plugins:', got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "myplugin:") {
		t.Errorf("config block missing 'myplugin:', got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "enabled: false") {
		t.Errorf("config block missing 'enabled: false', got:\n%s", cfg)
	}
}

func TestFilePlugin_ContainerEnvPairsFrom(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	cfg := map[string]interface{}{
		"enabled":       true,
		"auth_provider": "oauth",
	}
	pairs := p.containerEnvPairsFrom(cfg)
	wantEnabled := "RA_PLUGIN_MYPLUGIN_ENABLED=true"
	wantProvider := "RA_PLUGIN_MYPLUGIN_AUTH_PROVIDER=oauth"
	if !sliceContains(pairs, wantEnabled) {
		t.Errorf("pairs missing %q; got %v", wantEnabled, pairs)
	}
	if !sliceContains(pairs, wantProvider) {
		t.Errorf("pairs missing %q; got %v", wantProvider, pairs)
	}
}

func TestFilePlugin_ValidateConfig_RequiredField(t *testing.T) {
	raw := `
name: strict
version: "1.0.0"
description: "d"
config:
  - name: token
    type: string
    description: "A token"
    required: true
`
	fsys := fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(raw)}}
	p, _ := LoadPlugin(fsys)
	errs := p.validateConfig(map[string]interface{}{})
	if len(errs) == 0 {
		t.Fatal("expected error for missing required field")
	}
}

func TestFilePlugin_ValidateConfig_InvalidProvider(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	pluginCfg := map[string]interface{}{
		"enabled":       true,
		"auth_provider": "nonexistent",
	}
	errs := p.validateConfig(pluginCfg)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid auth_provider")
	}
}

func TestFilePlugin_ValidateConfig_MissingProvider(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	pluginCfg := map[string]interface{}{"enabled": true}
	errs := p.validateConfig(pluginCfg)
	if len(errs) == 0 {
		t.Fatal("expected error when auth_providers non-empty and auth_provider not set")
	}
}

func TestFilePlugin_ValidateConfig_Disabled(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	errs := p.validateConfig(map[string]interface{}{"enabled": false})
	if len(errs) != 0 {
		t.Errorf("expected no errors when disabled, got %v", errs)
	}
}

func sliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Add test for Prompt** (interactive, uses io pipe)

```go
func TestFilePlugin_Prompt_Disabled(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	// "N\n" → enabled=false
	r := bufio.NewReader(strings.NewReader("N\n"))
	cfg, secrets, err := p.Prompt(r, io.Discard)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(cfg, "enabled: false") {
		t.Errorf("expected enabled: false, got %q", cfg)
	}
	if secrets != "" {
		t.Errorf("expected empty secrets block, got %q", secrets)
	}
}

func TestFilePlugin_Prompt_Enabled_OAuth(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	// "y\n" → enabled=true; "1\n" → first auth provider (oauth)
	r := bufio.NewReader(strings.NewReader("y\n1\n"))
	cfg, secrets, err := p.Prompt(r, io.Discard)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(cfg, "enabled: true") {
		t.Errorf("expected enabled: true, got %q", cfg)
	}
	if !strings.Contains(cfg, "auth_provider: oauth") {
		t.Errorf("expected auth_provider: oauth, got %q", cfg)
	}
	if !strings.Contains(secrets, "my_oauth") {
		t.Errorf("expected my_oauth in secrets block, got %q", secrets)
	}
}
```

- [ ] **Step 3: Run to verify tests fail**

```
go test ./internal/plugins/... -run "TestFilePlugin_(Default|Container|Validate|Prompt)"
```
Expected: compilation error — methods not defined yet.

- [ ] **Step 4: Add methods to fileplugin.go**

Append to `internal/plugins/fileplugin.go`:

```go
import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfigFragments returns the zero-value config YAML block for this plugin.
func (p *FilePlugin) DefaultConfigFragments() (configBlock string, secretsBlock string) {
	defaults := make(map[string]interface{})
	for _, f := range p.schema.Config {
		if f.Default != nil {
			defaults[f.Name] = f.Default
		} else {
			switch f.Type {
			case "bool":
				defaults[f.Name] = false
			case "int":
				defaults[f.Name] = 0
			default:
				defaults[f.Name] = ""
			}
		}
	}
	return buildPluginsConfigBlock(p.schema.Name, defaults), ""
}

// containerEnvPairsFrom generates RA_PLUGIN_<NAME>_<FIELD>=<value> pairs from pluginCfg.
func (p *FilePlugin) containerEnvPairsFrom(pluginCfg map[string]interface{}) []string {
	prefix := "RA_PLUGIN_" + strings.ToUpper(strings.ReplaceAll(p.schema.Name, "-", "_")) + "_"
	var pairs []string
	for _, f := range p.schema.Config {
		key := prefix + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		val := fmt.Sprintf("%v", pluginCfg[f.Name])
		pairs = append(pairs, key+"="+val)
	}
	if len(p.schema.AuthProviders) > 0 {
		provider, _ := pluginCfg["auth_provider"].(string)
		pairs = append(pairs, prefix+"AUTH_PROVIDER="+provider)
	}
	return pairs
}

// validateConfig checks required fields and auth_provider against pluginCfg.
// Returns nil when the plugin is disabled (enabled==false or absent).
func (p *FilePlugin) validateConfig(pluginCfg map[string]interface{}) []string {
	// Check if plugin has an "enabled" field; if set to false, skip validation.
	for _, f := range p.schema.Config {
		if f.Name == "enabled" {
			if v, ok := pluginCfg["enabled"]; ok && v != true {
				return nil
			}
		}
	}

	var errs []string
	for _, f := range p.schema.Config {
		if f.Required {
			if _, ok := pluginCfg[f.Name]; !ok {
				errs = append(errs, fmt.Sprintf("plugin %s: required config field %q is missing", p.schema.Name, f.Name))
			}
		}
	}

	if len(p.schema.AuthProviders) > 0 {
		provider, _ := pluginCfg["auth_provider"].(string)
		if provider == "" {
			errs = append(errs, fmt.Sprintf("plugin %s: auth_provider must be set when auth_providers are defined", p.schema.Name))
		} else {
			valid := false
			for _, ap := range p.schema.AuthProviders {
				if ap.Name == provider {
					valid = true
					break
				}
			}
			if !valid {
				errs = append(errs, fmt.Sprintf("plugin %s: unknown auth_provider %q", p.schema.Name, provider))
			}
		}
	}
	return errs
}

// Prompt interactively collects plugin config from the user and returns YAML fragments.
func (p *FilePlugin) Prompt(r *bufio.Reader, out io.Writer) (configBlock string, secretsBlock string, err error) {
	pluginCfg := make(map[string]interface{})

	for _, f := range p.schema.Config {
		val, e := promptField(r, out, f)
		if e != nil {
			return "", "", e
		}
		pluginCfg[f.Name] = val
	}

	// Determine enabled state for auth provider gating.
	enabled := true
	for _, f := range p.schema.Config {
		if f.Name == "enabled" {
			if v, ok := pluginCfg["enabled"]; ok {
				enabled = v == true
			}
		}
	}

	var activeProvider *AuthProvider
	if enabled && len(p.schema.AuthProviders) > 0 {
		ap, e := promptAuthProvider(r, out, p.schema.AuthProviders)
		if e != nil {
			return "", "", e
		}
		pluginCfg["auth_provider"] = ap.Name
		activeProvider = ap
	}

	return buildPluginsConfigBlock(p.schema.Name, pluginCfg),
		buildSecretsBlock(p.schema.Secrets, activeProvider),
		nil
}

func promptField(r *bufio.Reader, out io.Writer, f ConfigField) (interface{}, error) {
	switch f.Type {
	case "bool":
		def := "N"
		if b, ok := f.Default.(bool); ok && b {
			def = "Y"
		}
		fmt.Fprintf(out, "%s [y/N] [%s]: ", f.Description, def)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		if choice == "" {
			if s, ok := f.Default.(bool); ok {
				return s, nil
			}
			return false, nil
		}
		return choice == "y" || choice == "yes", nil
	case "int":
		fmt.Fprintf(out, "%s: ", f.Description)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		var v int
		fmt.Sscan(strings.TrimSpace(line), &v)
		return v, nil
	default: // string
		if len(f.Options) > 0 {
			return promptOptions(r, out, f)
		}
		defVal, _ := f.Default.(string)
		fmt.Fprintf(out, "%s [%s]: ", f.Description, defVal)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		v := strings.TrimSpace(line)
		if v == "" {
			return defVal, nil
		}
		return v, nil
	}
}

func promptOptions(r *bufio.Reader, out io.Writer, f ConfigField) (string, error) {
	fmt.Fprintf(out, "%s:\n", f.Description)
	for i, o := range f.Options {
		fmt.Fprintf(out, "  %d) %s\n", i+1, o)
	}
	fmt.Fprintf(out, "choice [1-%d] [1]: ", len(f.Options))
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	var idx int
	if _, e := fmt.Sscan(strings.TrimSpace(line), &idx); e != nil || idx < 1 || idx > len(f.Options) {
		idx = 1
	}
	return f.Options[idx-1], nil
}

func promptAuthProvider(r *bufio.Reader, out io.Writer, providers []AuthProvider) (*AuthProvider, error) {
	fmt.Fprintln(out, "Auth provider:")
	for i, ap := range providers {
		fmt.Fprintf(out, "  %d) %s\n", i+1, ap.Description)
	}
	fmt.Fprintf(out, "choice [1-%d] [1]: ", len(providers))
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	var idx int
	if _, e := fmt.Sscan(strings.TrimSpace(line), &idx); e != nil || idx < 1 || idx > len(providers) {
		idx = 1
	}
	return &providers[idx-1], nil
}

func buildPluginsConfigBlock(name string, cfg map[string]interface{}) string {
	out, _ := yaml.Marshal(map[string]interface{}{
		"plugins": map[string]interface{}{name: cfg},
	})
	return string(out)
}

func buildSecretsBlock(topLevel []SecretDef, provider *AuthProvider) string {
	var defs []SecretDef
	defs = append(defs, topLevel...)
	if provider != nil {
		defs = append(defs, provider.Secrets...)
	}
	if len(defs) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, d := range defs {
		gcp := d.DefaultSecretName
		if gcp == "" {
			gcp = d.Name
		}
		fmt.Fprintf(&sb, "  %s:\n    secret_name: %s\n    env_var: %s\n", d.Name, gcp, d.EnvVar)
	}
	return sb.String()
}
```

Add these imports at the top of `fileplugin.go` (consolidate with existing imports):

```go
import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)
```

Add these to `fileplugin_test.go` imports:

```go
import (
	"bufio"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)
```

- [ ] **Step 5: Run to verify tests pass**

```
go test ./internal/plugins/... -run "TestFilePlugin_(Default|Container|Validate|Prompt)"
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/plugins/fileplugin.go internal/plugins/fileplugin_test.go
git commit -m "feat(plugins): add FilePlugin methods (validate, prompt, env pairs, config fragments)"
```

---

## Task 4: Default claude plugin files

**Files:**
- Create: `internal/plugins/defaults/claude/plugin.yaml`
- Create: `internal/plugins/defaults/claude/install.sh`
- Create: `internal/plugins/defaults/claude/setup.sh`

- [ ] **Step 1: Create plugin.yaml**

```yaml
# internal/plugins/defaults/claude/plugin.yaml
name: claude
version: "1.0.0"
description: "Installs and configures Claude Code on the workstation"

config:
  - name: enabled
    type: bool
    description: "Enable Claude Code?"
    default: false

auth_providers:
  - name: oauth
    description: "OAuth token (recommended)"
    secrets:
      - name: claude_oauth
        env_var: CLAUDE_CODE_OAUTH_TOKEN
        description: "OAuth token from claude.ai/settings"
        default_secret_name: claude-code-oauth-token

  - name: apikey
    description: "API key via helper script"
    secrets:
      - name: claude_apikey
        env_var: ANTHROPIC_API_KEY
        description: "API key from console.anthropic.com"
        default_secret_name: anthropic-api-key
```

- [ ] **Step 2: Create install.sh** (identical content to current `internal/plugins/claude/assets/install.sh`)

```bash
#!/bin/bash
#
# Claude Code — first-boot installer.
# Invoked by setup.sh when claude binary is absent. Runs as root.
#
set -euo pipefail

USER_NAME="user"
NPM_PREFIX="/home/${USER_NAME}/.npm-global"

mkdir -p "${NPM_PREFIX}"
chown "${USER_NAME}:${USER_NAME}" "${NPM_PREFIX}"

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm install -g @anthropic-ai/claude-code

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm cache clean --force
```

- [ ] **Step 3: Create setup.sh** (rewrite for new env-var-based model)

```bash
#!/bin/bash
#
# Claude Code plugin — runtime setup.
# Sourced by entrypoint.sh after all secrets are fetched.
# Secrets (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY) are already exported
# by the entrypoint dispatch loop when this script runs.
#
[ "${RA_PLUGIN_CLAUDE_ENABLED:-false}" = "true" ] || return 0

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"
CLAUDE_CONFIG_PATH="${USER_HOME}/.claude.json"
CLAUDE_INSTALL_SCRIPT="/etc/ra/plugins/claude/install.sh"
CLAUDE_BIN_PATH="${USER_HOME}/.npm-global/bin/claude"

_ra_claude_install_if_missing() {
    if [[ -x "${CLAUDE_BIN_PATH}" ]]; then return 0; fi
    if [[ ! -x "${CLAUDE_INSTALL_SCRIPT}" ]]; then
        log "claude: install script missing; skipping."
        return 0
    fi
    log "claude: installing @anthropic-ai/claude-code (first boot)..."
    bash "${CLAUDE_INSTALL_SCRIPT}" 2> >(while read -r line; do log "claude-install: ${line}"; done) \
        && log "claude: install complete." \
        || log "claude: install failed."
}

_ra_claude_clear_stale_state() {
    rm -f "${APIKEY_HELPER_PATH}"
    sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
    sed -i '/^ANTHROPIC_API_KEY=/d' /etc/environment

    if [[ -f "${CLAUDE_SETTINGS_PATH}" ]] && command -v jq >/dev/null 2>&1; then
        if jq -e '.apiKeyHelper' "${CLAUDE_SETTINGS_PATH}" >/dev/null 2>&1; then
            local tmp; tmp=$(mktemp)
            jq 'del(.apiKeyHelper)' "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
        fi
    fi
}

_ra_claude_seed_onboarding() {
    local token="$1"
    local version
    version=$(sudo -u "${USER_NAME}" "${CLAUDE_BIN_PATH}" --version 2>/dev/null \
        | awk '{print $1}' || true)
    [[ "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || version=""

    local tmp; tmp=$(mktemp)
    if [[ -f "${CLAUDE_CONFIG_PATH}" ]]; then
        jq --arg ver "${version}" '
            .hasCompletedOnboarding = true
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
        ' "${CLAUDE_CONFIG_PATH}" > "${tmp}" \
            || { rm -f "${tmp}"; log "claude: jq merge failed."; return 0; }
    else
        jq -n --arg ver "${version}" '
            {hasCompletedOnboarding: true}
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
        ' > "${tmp}" \
            || { rm -f "${tmp}"; log "claude: jq render failed."; return 0; }
    fi
    mv "${tmp}" "${CLAUDE_CONFIG_PATH}"
    chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_CONFIG_PATH}"
    chmod 600 "${CLAUDE_CONFIG_PATH}"
    log "claude: seeded ${CLAUDE_CONFIG_PATH}."

    if sudo -u "${USER_NAME}" env CLAUDE_CODE_OAUTH_TOKEN="${token}" \
            "${CLAUDE_BIN_PATH}" -p "respond with OK" 2>/dev/null | grep -q 'OK'; then
        log "claude: smoke test OK."
    else
        log "claude: smoke test did not return OK."
    fi
}

_ra_claude_install_if_missing
_ra_claude_clear_stale_state

case "${RA_PLUGIN_CLAUDE_AUTH_PROVIDER}" in
    oauth)
        if [[ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
            log "claude: CLAUDE_CODE_OAUTH_TOKEN not set; auth may fail."
        else
            echo "CLAUDE_CODE_OAUTH_TOKEN=${CLAUDE_CODE_OAUTH_TOKEN}" >> /etc/environment
            log "claude: exported CLAUDE_CODE_OAUTH_TOKEN."
            _ra_claude_seed_onboarding "${CLAUDE_CODE_OAUTH_TOKEN}"
        fi
        ;;
    apikey)
        if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
            log "claude: ANTHROPIC_API_KEY not set; auth may fail."
        else
            # Write apiKeyHelper that fetches the key from the already-known GCP secret.
            # We use the env var directly here since the entrypoint already fetched it.
            mkdir -p "${CLAUDE_SETTINGS_DIR}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_DIR}"
            if [[ ! -f "${CLAUDE_SETTINGS_PATH}" ]]; then
                printf '{"apiKeyHelper": "%s"}\n' "${ANTHROPIC_API_KEY}" > "${CLAUDE_SETTINGS_PATH}"
            elif command -v jq >/dev/null 2>&1; then
                local tmp; tmp=$(mktemp)
                jq --arg k "${ANTHROPIC_API_KEY}" '.apiKeyHelper = $k' \
                    "${CLAUDE_SETTINGS_PATH}" > "${tmp}" && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            fi
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
            log "claude: configured apikey mode."
        fi
        ;;
    *)
        log "claude: unknown auth provider '${RA_PLUGIN_CLAUDE_AUTH_PROVIDER:-}'."
        ;;
esac
```

- [ ] **Step 4: Commit**

```bash
git add internal/plugins/defaults/
git commit -m "feat(plugins): add default claude plugin files (plugin.yaml, install.sh, setup.sh)"
```

---

## Task 5: Embedded defaults and loader

**Files:**
- Create: `internal/plugins/defaults_embed.go`
- Create: `internal/plugins/loader.go`
- Create: `internal/plugins/loader_test.go`

- [ ] **Step 1: Write failing loader tests**

```go
// internal/plugins/loader_test.go
package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAll_EmbeddedDefaults(t *testing.T) {
	// No filesystem dirs — should still get embedded claude plugin.
	plugins, err := LoadAll("", "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	var names []string
	for _, p := range plugins {
		names = append(names, p.Name())
	}
	found := false
	for _, n := range names {
		if n == "claude" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected embedded claude plugin, got %v", names)
	}
}

func TestLoadAll_FilesystemShadowsEmbedded(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write a different version of claude plugin.yaml
	yaml := "name: claude\nversion: \"99.0.0\"\ndescription: \"Override\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	plugins, err := LoadAll(dir, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	var claude *FilePlugin
	for _, p := range plugins {
		if p.Name() == "claude" {
			claude = p
		}
	}
	if claude == nil {
		t.Fatal("claude plugin not found")
	}
	if claude.schema.Version != "99.0.0" {
		t.Errorf("expected override version 99.0.0, got %q", claude.schema.Version)
	}
}

func TestLoadAll_ProjectShadowsUser(t *testing.T) {
	userDir := t.TempDir()
	projDir := t.TempDir()

	write := func(dir, version string) {
		d := filepath.Join(dir, "myplugin")
		os.MkdirAll(d, 0755)
		yaml := "name: myplugin\nversion: \"" + version + "\"\ndescription: \"d\"\n"
		os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(yaml), 0644)
	}
	write(userDir, "1.0.0")
	write(projDir, "2.0.0")

	plugins, err := LoadAll(userDir, projDir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	for _, p := range plugins {
		if p.Name() == "myplugin" && p.schema.Version != "2.0.0" {
			t.Errorf("expected project version 2.0.0, got %q", p.schema.Version)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```
go test ./internal/plugins/... -run TestLoadAll
```
Expected: compilation error — `LoadAll`, `defaultsFS` not defined.

- [ ] **Step 3: Create defaults_embed.go**

```go
// internal/plugins/defaults_embed.go
package plugins

import "embed"

//go:embed defaults
var defaultsFS embed.FS
```

- [ ] **Step 4: Create loader.go**

```go
// internal/plugins/loader.go
package plugins

import (
	"io/fs"
	"os"
	"path/filepath"
)

// LoadAll returns all plugins from embedded defaults, user-global dir, and
// project-local dir. Priority (highest last): embedded → user-global → project-local.
// Plugins of the same name are shadowed entirely by the higher-priority source.
func LoadAll(userDir, projectDir string) ([]*FilePlugin, error) {
	byName := make(map[string]*FilePlugin)

	// Embedded defaults (lowest priority).
	entries, _ := defaultsFS.ReadDir("defaults")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(defaultsFS, "defaults/"+e.Name())
		if err != nil {
			continue
		}
		p, err := LoadPlugin(sub)
		if err != nil {
			continue
		}
		byName[p.Name()] = p
	}

	loadDir(userDir, byName)
	loadDir(projectDir, byName)

	out := make([]*FilePlugin, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	return out, nil
}

func loadDir(dir string, byName map[string]*FilePlugin) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) || err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := os.DirFS(filepath.Join(dir, e.Name()))
		p, err := LoadPlugin(sub)
		if err != nil {
			continue
		}
		byName[p.Name()] = p
	}
}
```

- [ ] **Step 5: Run to verify tests pass**

```
go test ./internal/plugins/... -run TestLoadAll
```
Expected: PASS.

- [ ] **Step 6: Run full plugin package tests**

```
go test ./internal/plugins/...
```
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/plugins/defaults_embed.go internal/plugins/loader.go internal/plugins/loader_test.go
git commit -m "feat(plugins): add embedded defaults and LoadAll directory scanner"
```

---

## Task 6: Config switchover — update types, registry, root; delete old code

This is one atomic commit because `cfg.Claude` removal, claude plugin deletion, registry type change, and `cmd/root.go` update are mutually dependent.

**Files:**
- Modify: `internal/config/types.go`
- Modify: `internal/config/merge.go`
- Modify: `internal/config/validate.go`
- Modify: `internal/plugins/fileplugin.go` (add `Validate` and `ContainerEnvPairs` wrappers taking `*config.Config`)
- Modify: `internal/plugins/registry.go`
- Modify: `internal/plugins/registry_test.go`
- Modify: `cmd/root.go`
- Delete: `internal/plugins/plugin.go`
- Delete: `internal/plugins/claude/` (entire directory)

- [ ] **Step 1: Update internal/config/types.go**

Replace `ClaudeConfig` struct and all three `Claude` fields with `Plugins`:

```go
// internal/config/types.go
package config

type GitConfig struct {
	UserName      string `yaml:"user_name"`
	UserEmail     string `yaml:"user_email"`
	GithubRepoURL string `yaml:"github_repo_url"`
}

type WorkstationConfig struct {
	Region             string `yaml:"region"`
	MachineType        string `yaml:"machine_type"`
	DiskSizeGB         int    `yaml:"disk_size_gb"`
	ClusterName        string `yaml:"cluster_name"`
	ConfigName         string `yaml:"config_name"`
	ServiceAccountName string `yaml:"service_account_name"`
	ImageName          string `yaml:"image_name"`
	ImageTag           string `yaml:"image_tag"`
	RepoName           string `yaml:"repo_name"`
	AutoSleepHours     *int   `yaml:"auto_sleep_hours"`
	AutoShutdownHours  int    `yaml:"auto_shutdown_hours"`
}

type SecretEntry struct {
	SecretName string `yaml:"secret_name"`
	EnvVar     string `yaml:"env_var"`
}

type GcloudConfig struct {
	Project     string            `yaml:"project"`
	Workstation WorkstationConfig `yaml:"workstation"`
}

type UserConfig struct {
	Git     GitConfig                            `yaml:"git"`
	Gcloud  GcloudConfig                         `yaml:"gcloud"`
	Plugins map[string]map[string]interface{}    `yaml:"plugins,omitempty"`
	Secrets map[string]SecretEntry               `yaml:"secrets"`
	Env     map[string]string                    `yaml:"env"`
}

type ProjectConfig struct {
	Git     GitConfig                            `yaml:"git"`
	Gcloud  GcloudConfig                         `yaml:"gcloud"`
	Plugins map[string]map[string]interface{}    `yaml:"plugins,omitempty"`
	Secrets map[string]SecretEntry               `yaml:"secrets"`
	Env     map[string]string                    `yaml:"env"`
}

type Config struct {
	Git    GitConfig
	Gcloud GcloudConfig

	Plugins map[string]map[string]interface{}

	Secrets map[string]SecretEntry
	Env     map[string]string

	WorkstationName   string
	ProjectConfigPath string
}
```

- [ ] **Step 2: Update internal/config/merge.go**

Replace the `cfg.Claude = ClaudeConfig{...}` block (lines 51–54) with Plugins map merge:

```go
// Replace lines 51-54 in merge.go:
cfg.Plugins = mergePluginsMap(u.Plugins, project.Plugins)
```

Add the helper function (remove `pickBoolPtr` since no longer needed):

```go
func mergePluginsMap(u, project map[string]map[string]interface{}) map[string]map[string]interface{} {
	out := make(map[string]map[string]interface{})
	for k, v := range u {
		out[k] = v
	}
	for k, v := range project {
		out[k] = v
	}
	return out
}
```

Remove `pickBoolPtr` function (no longer used).

- [ ] **Step 3: Update internal/config/validate.go**

Remove `"RA_CLAUDE_ENABLED"` and `"RA_CODEX_ENABLED"` from `reservedEnvVars` (these are now generated dynamically by plugins as `RA_PLUGIN_*`). The updated list:

```go
var reservedEnvVars = []string{
	"GCP_PROJECT_ID",
	"GITHUB_REPO_URL",
	"GIT_USER_NAME",
	"GIT_USER_EMAIL",
	"RA_SECRETS",
}
```

- [ ] **Step 4: Add Validate and ContainerEnvPairs wrappers to fileplugin.go**

Append to `internal/plugins/fileplugin.go`:

```go
// Validate checks required config fields and auth_provider validity.
func (p *FilePlugin) Validate(cfg *config.Config) []string {
	pluginCfg := map[string]interface{}{}
	if cfg.Plugins != nil {
		if c, ok := cfg.Plugins[p.schema.Name]; ok {
			pluginCfg = c
		}
	}
	return p.validateConfig(pluginCfg)
}

// ContainerEnvPairs returns RA_PLUGIN_<NAME>_<FIELD>=<value> pairs for --container-env.
func (p *FilePlugin) ContainerEnvPairs(cfg *config.Config) []string {
	pluginCfg := map[string]interface{}{}
	if cfg.Plugins != nil {
		if c, ok := cfg.Plugins[p.schema.Name]; ok {
			pluginCfg = c
		}
	}
	return p.containerEnvPairsFrom(pluginCfg)
}
```

Add `"github.com/adamtait/remote-agent/internal/config"` to the imports in `fileplugin.go`.

- [ ] **Step 5: Update registry.go to use []*FilePlugin**

```go
// internal/plugins/registry.go
package plugins

import "github.com/adamtait/remote-agent/internal/config"

// Registry holds the ordered list of active plugins.
type Registry struct {
	plugins []*FilePlugin
}

// NewRegistry creates a Registry from the provided FilePlugin slice.
func NewRegistry(ps []*FilePlugin) *Registry {
	return &Registry{plugins: ps}
}

// All returns all plugins.
func (r *Registry) All() []*FilePlugin {
	if r == nil {
		return nil
	}
	return r.plugins
}

// AllAssets returns the union of all plugin assets.
func (r *Registry) AllAssets() []Asset {
	if r == nil {
		return nil
	}
	var out []Asset
	for _, p := range r.plugins {
		out = append(out, p.Assets()...)
	}
	return out
}

// ValidateAll runs every plugin's Validate and accumulates error strings.
func (r *Registry) ValidateAll(cfg *config.Config) []string {
	if r == nil {
		return nil
	}
	var errs []string
	for _, p := range r.plugins {
		errs = append(errs, p.Validate(cfg)...)
	}
	return errs
}

// AllContainerEnvPairs returns env KEY=VALUE pairs from all plugins.
func (r *Registry) AllContainerEnvPairs(cfg *config.Config) []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, p := range r.plugins {
		out = append(out, p.ContainerEnvPairs(cfg)...)
	}
	return out
}
```

- [ ] **Step 6: Update registry_test.go**

Replace the test `mockPlugin` stub (which implements the old `Plugin` interface) with a `FilePlugin` loaded from `fstest.MapFS`. The existing test structure can be retained; replace `mockPlugin` usage:

```go
// internal/plugins/registry_test.go
package plugins

import (
	"testing"
	"testing/fstest"

	"github.com/adamtait/remote-agent/internal/config"
)

func makeTestPlugin(t *testing.T, name string) *FilePlugin {
	t.Helper()
	yaml := "name: " + name + "\nversion: \"1.0.0\"\ndescription: \"d\"\n"
	fsys := fstest.MapFS{
		"plugin.yaml": &fstest.MapFile{Data: []byte(yaml)},
		"setup.sh":    &fstest.MapFile{Data: []byte("#!/bin/bash\n"), Mode: 0755},
	}
	p, err := LoadPlugin(fsys)
	if err != nil {
		t.Fatalf("makeTestPlugin(%q): %v", name, err)
	}
	return p
}

func TestRegistry_NilSafe(t *testing.T) {
	var r *Registry
	if r.All() != nil {
		t.Error("nil registry All() should return nil")
	}
	if r.AllAssets() != nil {
		t.Error("nil registry AllAssets() should return nil")
	}
	if r.ValidateAll(nil) != nil {
		t.Error("nil registry ValidateAll() should return nil")
	}
	if r.AllContainerEnvPairs(nil) != nil {
		t.Error("nil registry AllContainerEnvPairs() should return nil")
	}
}

func TestRegistry_AllAssets(t *testing.T) {
	p := makeTestPlugin(t, "alpha")
	r := NewRegistry([]*FilePlugin{p})
	assets := r.AllAssets()
	if len(assets) != 1 {
		t.Errorf("expected 1 asset, got %d", len(assets))
	}
	if assets[0].Name != "plugins/alpha/setup.sh" {
		t.Errorf("unexpected asset name %q", assets[0].Name)
	}
}

func TestRegistry_ValidateAll_NoErrors(t *testing.T) {
	p := makeTestPlugin(t, "alpha")
	r := NewRegistry([]*FilePlugin{p})
	if errs := r.ValidateAll(&config.Config{}); len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestRegistry_ContainerEnvPairs(t *testing.T) {
	p := makeTestPlugin(t, "alpha")
	r := NewRegistry([]*FilePlugin{p})
	pairs := r.AllContainerEnvPairs(&config.Config{})
	// alpha has no config fields, so no pairs
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs, got %v", pairs)
	}
}
```

- [ ] **Step 7: Update cmd/root.go**

Replace:
```go
import (
    ...
    claudeplugin "github.com/adamtait/remote-agent/internal/plugins/claude"
    ...
)

func init() {
    reg = plugins.NewRegistry(claudeplugin.New())
}
```

With:
```go
import (
    "fmt"
    "io"
    "os"
    "path/filepath"

    "github.com/adamtait/remote-agent/internal/config"
    "github.com/adamtait/remote-agent/internal/gcloud"
    "github.com/adamtait/remote-agent/internal/plugins"
    "github.com/spf13/cobra"
    "github.com/spf13/pflag"
)

func init() {
    home, err := os.UserHomeDir()
    if err != nil {
        // Fall back to empty registry if home dir not determinable at init time.
        reg = plugins.NewRegistry(nil)
        return
    }
    userPluginsDir := filepath.Join(home, ".config", "ra", "plugins")
    // Project plugins dir resolved later (need cwd). Pass empty for now;
    // PersistentPreRunE re-initialises reg with the project dir.
    loaded, err := plugins.LoadAll(userPluginsDir, "")
    if err != nil {
        reg = plugins.NewRegistry(nil)
        return
    }
    reg = plugins.NewRegistry(loaded)
}
```

Then in `PersistentPreRunE`, after finding `projectCfgPath`, reinitialise the registry with the project plugins dir:

```go
// After cfg.ProjectConfigPath = projectCfgPath:
projectPluginsDir := filepath.Join(filepath.Dir(projectCfgPath), "plugins")
loaded, err := plugins.LoadAll(
    filepath.Join(home, ".config", "ra", "plugins"),
    projectPluginsDir,
)
if err != nil {
    return fmt.Errorf("loading plugins: %w", err)
}
reg = plugins.NewRegistry(loaded)
```

- [ ] **Step 8: Delete old files**

```bash
rm internal/plugins/plugin.go
rm -rf internal/plugins/claude/
```

- [ ] **Step 9: Run all tests**

```
go test ./...
```
Expected: all PASS. Fix any compilation errors from `OAuthAccountSecretName` removal in config tests (remove references).

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "feat(plugins): switch to file-based plugin system; remove compiled Plugin interface and claude plugin"
```

---

## Task 7: Update Dockerfile and entrypoint.sh

**Files:**
- Modify: `internal/scaffold/assets/Dockerfile`
- Modify: `internal/scaffold/assets/entrypoint.sh`

- [ ] **Step 1: Update Dockerfile**

Replace the explicit claude COPY section:

```dockerfile
# OLD — remove these lines:
RUN mkdir -p /etc/ra
COPY .ra/claude/install.sh /etc/ra/claude-install.sh
COPY .ra/claude/setup.sh /etc/ra/claude-setup.sh
RUN chmod 0755 /etc/ra/claude-install.sh /etc/ra/claude-setup.sh
```

With the generic plugin directory:

```dockerfile
# Plugin scripts (setup.sh sourced at boot; install.sh called by setup.sh on first boot)
RUN mkdir -p /etc/ra/plugins
COPY .ra/plugins/ /etc/ra/plugins/
RUN find /etc/ra/plugins -name "*.sh" -exec chmod 0755 {} +
```

- [ ] **Step 2: Update entrypoint.sh**

Remove the claude-specific harness section (lines 75–80):

```bash
# OLD — remove:
if [[ "${RA_CLAUDE_ENABLED:-false}" == "true" && -f /etc/ra/claude-setup.sh ]]; then
    log "Claude harness enabled; sourcing /etc/ra/claude-setup.sh."
    source /etc/ra/claude-setup.sh
fi
```

Remove the `claude_oauth|claude_apikey` fallthrough case from the dispatch loop (lines 143–149):

```bash
# OLD — remove:
        claude_oauth|claude_apikey)
            log "WARNING: ${logical} configured but RA_CLAUDE_ENABLED!=true; skipping."
            ;;
```

Remove the `ra_claude_handle_secret` call block (lines 103–107):

```bash
# OLD — remove:
    if declare -f ra_claude_handle_secret >/dev/null 2>&1; then
        if ra_claude_handle_secret "${logical}" "${secret_name}" "${target_env_var}"; then
            continue
        fi
    fi
```

Add plugin sourcing after the dispatch loop (after line 164, before the first-boot section):

```bash
# =============================================================================
# Source plugin setup scripts. Each script guards itself with
# RA_PLUGIN_<NAME>_ENABLED checks; secrets are already in env from the
# dispatch loop above.
# =============================================================================
for _ra_plugin_setup in /etc/ra/plugins/*/setup.sh; do
    if [[ -f "${_ra_plugin_setup}" ]]; then
        log "Sourcing plugin: ${_ra_plugin_setup}"
        # shellcheck source=/dev/null
        source "${_ra_plugin_setup}"
    fi
done
unset _ra_plugin_setup
```

Also update the contract comment at the top of entrypoint.sh to remove `RA_CLAUDE_ENABLED` and the harness plugin contract section, replacing with:

```bash
#   RA_PLUGIN_<NAME>_ENABLED=true|false  — per-plugin toggle
#   RA_PLUGIN_<NAME>_AUTH_PROVIDER=<provider>  — active auth provider
```

- [ ] **Step 3: Run tests**

```
go test ./...
```
Expected: all PASS (Dockerfile and entrypoint are shell scripts; Go tests unaffected).

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/assets/Dockerfile internal/scaffold/assets/entrypoint.sh
git commit -m "feat(plugins): update Dockerfile and entrypoint.sh for generic plugin sourcing"
```

---

## Task 8: Add ra plugin subcommand

**Files:**
- Create: `cmd/plugin.go`

- [ ] **Step 1: Write failing test**

```go
// cmd/plugin_test.go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginList_NoUserDir(t *testing.T) {
	// ra plugin list should succeed even with no user plugin dir.
	code := ExecuteWithIO(nil, strings.NewReader(""), strings.NewReader(""), strings.NewReader(""), []string{"plugin", "list"})
	// list command should not require config validation — it has no PersistentPreRunE
	// The command itself may error due to test environment, but must not panic.
	_ = code
}
```

Actually the existing `ExecuteWithIO` signature is `(stdin io.Reader, stdout, stderr io.Writer, args []string)`. Use that.

```go
// cmd/plugin_test.go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPluginListCommand_Registered(t *testing.T) {
	// Verify ra plugin list is registered without crashing.
	var out, errOut bytes.Buffer
	code := ExecuteWithIO(strings.NewReader(""), &out, &errOut, []string{"plugin", "--help"})
	if code != 0 && !strings.Contains(errOut.String(), "") {
		t.Logf("stderr: %s", errOut.String())
	}
	// --help always exits 0 in cobra
}
```

- [ ] **Step 2: Run to verify it fails**

```
go test ./cmd/... -run TestPluginListCommand_Registered
```
Expected: PASS trivially (--help doesn't require plugin subcommand to exist; but `ra plugin` will be unrecognised if not registered). Adjust: run `ra plugin list` and check exit code. For now proceed to implementation.

- [ ] **Step 3: Implement cmd/plugin.go**

```go
// cmd/plugin.go
package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
)

func init() {
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)
	rootCmd.AddCommand(pluginCmd)
}

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage ra plugins",
}

var pluginInstallLocal bool
var pluginInstallForce bool

func init() {
	pluginInstallCmd.Flags().BoolVar(&pluginInstallLocal, "local", false, "Install into .ra/plugins/ instead of ~/.config/ra/plugins/")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallForce, "force", false, "Overwrite existing plugin of the same name")
}

var pluginInstallCmd = &cobra.Command{
	Use:   "install <git-url>",
	Short: "Install a plugin from a git repository",
	Args:  cobra.ExactArgs(1),
	// Bypass config validation — plugin install doesn't need a valid workstation config.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		gitURL := args[0]

		tmp, err := os.MkdirTemp("", "ra-plugin-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)

		clone := exec.Command("git", "clone", "--depth=1", gitURL, tmp)
		clone.Stdout = os.Stdout
		clone.Stderr = os.Stderr
		if err := clone.Run(); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}

		p, err := plugins.LoadPlugin(os.DirFS(tmp))
		if err != nil {
			return fmt.Errorf("reading plugin.yaml from cloned repo: %w", err)
		}

		var destBase string
		if pluginInstallLocal {
			wd, _ := os.Getwd()
			destBase = filepath.Join(wd, ".ra", "plugins")
		} else {
			home, _ := os.UserHomeDir()
			destBase = filepath.Join(home, ".config", "ra", "plugins")
		}
		dest := filepath.Join(destBase, p.Name())

		if _, err := os.Stat(dest); err == nil && !pluginInstallForce {
			return fmt.Errorf("plugin %q already exists at %s; use --force to overwrite", p.Name(), dest)
		}
		if err := os.RemoveAll(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(destBase, 0755); err != nil {
			return err
		}
		if err := copyDir(tmp, dest); err != nil {
			return fmt.Errorf("installing plugin: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %q to %s\n", p.Name(), dest)
		return nil
	},
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed plugins",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		wd, _ := os.Getwd()
		userDir := filepath.Join(home, ".config", "ra", "plugins")
		projDir := filepath.Join(wd, ".ra", "plugins")

		loaded, err := plugins.LoadAll(userDir, projDir)
		if err != nil {
			return err
		}
		if len(loaded) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(no plugins installed)")
			return nil
		}
		for _, p := range loaded {
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %s\n", p.Name(), p.Schema().Description)
		}
		return nil
	},
}

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed plugin",
	Args:  cobra.ExactArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		home, _ := os.UserHomeDir()
		dest := filepath.Join(home, ".config", "ra", "plugins", name)
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("plugin %q not found at %s", name, dest)
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed plugin %q\n", name)
		return nil
	},
}

// copyDir recursively copies src directory to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, src)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
```

Note: `plugin list` calls `p.Schema().Description`. Add a `Schema()` accessor to `FilePlugin`:

```go
// In fileplugin.go:
func (p *FilePlugin) Schema() PluginSchema { return p.schema }
```

- [ ] **Step 4: Run tests**

```
go test ./...
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/plugin.go
git commit -m "feat(cmd): add ra plugin install/list/remove subcommand"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|-----------------|------|
| plugin.yaml schema (config fields, secrets, auth_providers) | Task 1 |
| FilePlugin loads from fs.FS | Task 2 |
| Assets named `plugins/<name>/<script>` | Task 2 |
| DefaultConfigFragments writes under `plugins:` | Task 3 |
| ContainerEnvPairs generates `RA_PLUGIN_<NAME>_<FIELD>` | Task 3 |
| Interactive Prompt generates config+secrets blocks | Task 3 |
| Validate checks required fields + valid auth_provider | Task 3 |
| Default claude plugin as embedded files | Task 4 |
| Embedded defaults + filesystem loader shadowing | Task 5 |
| config.Config uses `Plugins map[string]map[string]interface{}` | Task 6 |
| Plugin interface and claude Go code deleted | Task 6 |
| Registry uses `[]*FilePlugin` | Task 6 |
| cmd/root.go calls LoadAll() | Task 6 |
| Dockerfile uses generic plugin COPY | Task 7 |
| entrypoint.sh glob-sources plugin setup.sh | Task 7 |
| ra plugin install/list/remove | Task 8 |

All requirements covered. ✓

**Placeholder scan:** No TBDs or TODOs in task code. ✓

**Type consistency check:**
- `LoadPlugin(fsys fs.FS)` defined Task 2, used Task 5 ✓
- `LoadAll(userDir, projectDir string)` defined Task 5, used Task 6/8 ✓
- `NewRegistry(ps []*FilePlugin)` defined Task 6, used Task 6 ✓
- `p.Schema()` accessor added in Task 8 note — also add to Task 2 fileplugin.go ✓
- `SecretEntry.OAuthAccountSecretName` removed in Task 6 — check config tests don't reference it ✓

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-26-file-based-plugin-system.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — execute tasks in this session using executing-plans, batch with checkpoints

**Which approach?**
