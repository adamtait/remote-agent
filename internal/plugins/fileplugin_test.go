package plugins

import (
	"bufio"
	"io"
	"io/fs"
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

func sliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestFilePlugin_Prompt_Disabled(t *testing.T) {
	p, _ := LoadPlugin(testPluginFS(nil))
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
