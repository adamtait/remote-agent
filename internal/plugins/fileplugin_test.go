package plugins

import (
	"bufio"
	"encoding/base64"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

func TestFilePlugin_DefaultConfigFragments(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	cfg, secrets := p.DefaultConfigFragments()
	if secrets != "" {
		t.Errorf("secrets block should be empty, got %q", secrets)
	}
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

func TestFilePlugin_ValidateConfig_RequiredStringList_Empty(t *testing.T) {
	raw := `
name: strict
version: "1.0.0"
description: "d"
config:
  - name: targets
    type: string_list
    description: "Target list"
    required: true
`
	fsys := fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(raw)}}
	p, _ := LoadPlugin(fsys)

	// present but empty []string
	errs := p.validateConfig(map[string]interface{}{"targets": []string{}})
	if len(errs) == 0 {
		t.Fatal("expected error for empty required string_list ([]string)")
	}

	// present but empty []interface{}
	errs = p.validateConfig(map[string]interface{}{"targets": []interface{}{}})
	if len(errs) == 0 {
		t.Fatal("expected error for empty required string_list ([]interface{})")
	}

	// non-empty passes
	errs = p.validateConfig(map[string]interface{}{"targets": []string{"a"}})
	if len(errs) != 0 {
		t.Errorf("expected no errors for non-empty required string_list, got %v", errs)
	}
}

func TestFilePlugin_ValidateConfig_Disabled(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	errs := p.validateConfig(map[string]interface{}{"enabled": false})
	if len(errs) != 0 {
		t.Errorf("expected no errors when disabled, got %v", errs)
	}
}

func TestFilePlugin_ValidateConfig_EnabledAbsent(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	// No "enabled" key — should skip validation (treat as disabled)
	errs := p.validateConfig(map[string]interface{}{})
	if len(errs) != 0 {
		t.Errorf("expected no errors when enabled absent, got %v", errs)
	}
}

func TestFilePlugin_ContainerEnvPairsFrom_MissingField(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
	pairs := p.containerEnvPairsFrom(map[string]interface{}{}) // no keys
	for _, pair := range pairs {
		if strings.Contains(pair, "<nil>") {
			t.Errorf("pair contains <nil>: %q", pair)
		}
	}
}

func TestPromptField_StringList(t *testing.T) {
	f := ConfigField{Name: "repo_urls", Type: "string_list", Description: "Git repos"}
	r := bufio.NewReader(strings.NewReader("https://github.com/a/b\nhttps://github.com/c/d\n\n"))
	val, err := promptField(r, io.Discard, f)
	if err != nil {
		t.Fatalf("promptField: %v", err)
	}
	got, ok := val.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", val)
	}
	if len(got) != 2 || got[0] != "https://github.com/a/b" || got[1] != "https://github.com/c/d" {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestPromptField_StringList_Empty(t *testing.T) {
	f := ConfigField{Name: "repo_urls", Type: "string_list", Description: "Git repos"}
	r := bufio.NewReader(strings.NewReader("\n"))
	val, err := promptField(r, io.Discard, f)
	if err != nil {
		t.Fatalf("promptField: %v", err)
	}
	if val != nil {
		got, ok := val.([]string)
		if !ok {
			t.Fatalf("expected []string, got %T", val)
		}
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	}
}

func TestContainerEnvPairs_StringList(t *testing.T) {
	raw := `
name: ghtest
version: "1.0.0"
description: "d"
config:
  - name: repo_urls
    type: string_list
    description: "repos"
`
	fsys := fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(raw)}}
	p, _ := LoadPlugin(fsys)
	want := "RA_PLUGIN_GHTEST_REPO_URLS=https://github.com/a/b|https://github.com/c/d"

	pairs := p.containerEnvPairsFrom(map[string]interface{}{
		"repo_urls": []string{"https://github.com/a/b", "https://github.com/c/d"},
	})
	if !sliceContains(pairs, want) {
		t.Errorf("[]string: missing %q; got %v", want, pairs)
	}

	pairs2 := p.containerEnvPairsFrom(map[string]interface{}{
		"repo_urls": []interface{}{"https://github.com/a/b", "https://github.com/c/d"},
	})
	if !sliceContains(pairs2, want) {
		t.Errorf("[]interface{}: missing %q; got %v", want, pairs2)
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

// TestFilePlugin_Prompt_AlwaysEnabled verifies that `ra plugin install`
// auto-enables the plugin without prompting — users disable later by
// editing config.yaml.
func TestFilePlugin_Prompt_AlwaysEnabled(t *testing.T) {
	fsys := fstest.MapFS{
		"plugin.yaml": &fstest.MapFile{Data: []byte(testPluginNoSecretsYAML)},
	}
	p, _ := LoadPlugin(fsys)
	// "1\n" picks the first auth provider; no enable/disable prompt is shown.
	r := bufio.NewReader(strings.NewReader("1\n"))
	res, err := p.Prompt(r, io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if v, ok := res.Config["enabled"]; !ok || v != true {
		t.Errorf("expected Config[enabled]=true, got %v", res.Config["enabled"])
	}
	cfg := p.RenderConfigBlock(res)
	if !strings.Contains(cfg, "enabled: true") {
		t.Errorf("RenderConfigBlock: expected 'enabled: true', got %q", cfg)
	}
}

// testPluginNoSecretsYAML defines a plugin with auth providers but no secrets,
// so Prompt_Enabled tests don't require a real TTY for term.ReadPassword.
const testPluginNoSecretsYAML = `
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
`

func TestFilePlugin_Prompt_Enabled_OAuth(t *testing.T) {
	fsys := fstest.MapFS{
		"plugin.yaml": &fstest.MapFile{Data: []byte(testPluginNoSecretsYAML)},
	}
	p, _ := LoadPlugin(fsys)
	// `enabled` is auto-set to true (no prompt); "1\n" → first auth provider (oauth).
	r := bufio.NewReader(strings.NewReader("1\n"))
	res, err := p.Prompt(r, io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if v, ok := res.Config["enabled"]; !ok || v != true {
		t.Errorf("expected Config[enabled]=true, got %v", res.Config["enabled"])
	}
	if v, ok := res.Config["auth_provider"]; !ok || v != "oauth" {
		t.Errorf("expected Config[auth_provider]=oauth, got %v", res.Config["auth_provider"])
	}
	if res.AuthProvider == nil || res.AuthProvider.Name != "oauth" {
		t.Errorf("expected AuthProvider.Name=oauth, got %v", res.AuthProvider)
	}
	if len(res.SecretValues) != 0 {
		t.Errorf("expected no SecretValues (provider has no secrets), got %v", res.SecretValues)
	}
	// RenderConfigBlock should contain auth_provider: oauth
	cfg := p.RenderConfigBlock(res)
	if !strings.Contains(cfg, "auth_provider: oauth") {
		t.Errorf("RenderConfigBlock: expected 'auth_provider: oauth', got %q", cfg)
	}
	// RenderSecretsBlock should reflect provider secrets (none here)
	sec := p.RenderSecretsBlock(res)
	if sec != "" {
		t.Errorf("RenderSecretsBlock: expected empty (no secrets), got %q", sec)
	}
}

func TestFilePlugin_Prompt_SecretValueCommand_FileBase64(t *testing.T) {
	tmp := t.TempDir()
	authPath := filepath.Join(tmp, "auth.json")
	scriptPath := filepath.Join(tmp, "write-auth.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '{\"account\":\"ok\"}' > \"$1\"\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	raw := `
name: codex
version: "1.0.0"
description: "Codex"
config:
  - name: enabled
    type: bool
    description: "Enable?"
    default: false
auth_providers:
  - name: oauth
    description: "OAuth"
    secrets:
      - name: codex_auth_json
        env_var: CODEX_AUTH_JSON_B64
        default_secret_name: codex-auth-json
        value_command:
          command: ` + scriptPath + `
          args: ["` + authPath + `"]
          output: file_base64
          path: ` + authPath + `
          validate: json
          interactive: true
`
	p, err := LoadPlugin(fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(raw)}})
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	res, err := p.Prompt(bufio.NewReader(strings.NewReader("1\n")), io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	got := res.SecretValues["codex_auth_json"]
	want := base64.StdEncoding.EncodeToString([]byte(`{"account":"ok"}`))
	if got != want {
		t.Errorf("secret value = %q, want %q", got, want)
	}
}

func TestFilePlugin_Prompt_ConfigValueCommand_Stdout(t *testing.T) {
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "print-mode.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf generated-mode\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	raw := `
name: generated
version: "1.0.0"
description: "Generated config"
config:
  - name: enabled
    type: bool
    description: "Enable?"
    default: false
  - name: mode
    type: string
    description: "Mode"
    value_command:
      command: ` + scriptPath + `
      output: stdout
`
	p, err := LoadPlugin(fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(raw)}})
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	res, err := p.Prompt(bufio.NewReader(strings.NewReader("")), io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if got := res.Config["mode"]; got != "generated-mode" {
		t.Errorf("Config[mode] = %v, want generated-mode", got)
	}
}

func TestCodexPluginOAuthSecretUsesValueCommand(t *testing.T) {
	p, err := LoadPlugin(os.DirFS(filepath.Join("..", "..", "plugins", "codex")))
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	var secret *SecretDef
	for _, provider := range p.Schema().AuthProviders {
		if provider.Name != "oauth" {
			continue
		}
		for i := range provider.Secrets {
			if provider.Secrets[i].Name == "codex_auth_json" {
				secret = &provider.Secrets[i]
				break
			}
		}
	}
	if secret == nil {
		t.Fatal("codex oauth secret codex_auth_json not found")
	}
	if secret.ValueCommand == nil {
		t.Fatal("codex oauth secret missing value_command")
	}
	if secret.ValueCommand.Command != "codex" {
		t.Errorf("command = %q, want codex", secret.ValueCommand.Command)
	}
	if len(secret.ValueCommand.Args) != 1 || secret.ValueCommand.Args[0] != "login" {
		t.Errorf("args = %+v, want [login]", secret.ValueCommand.Args)
	}
	if secret.ValueCommand.Output != "file_base64" || secret.ValueCommand.Path != "~/.codex/auth.json" {
		t.Errorf("unexpected value command file settings: %+v", secret.ValueCommand)
	}
	if secret.ValueCommand.Validate != "json" || !secret.ValueCommand.Interactive {
		t.Errorf("unexpected value command validation/interactive: %+v", secret.ValueCommand)
	}
}
