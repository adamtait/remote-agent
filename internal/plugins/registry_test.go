package plugins

import (
	"os"
	"path/filepath"
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
	if r.ValidateAll(nil) != nil {
		t.Error("nil registry ValidateAll() should return nil")
	}
	if r.AllContainerEnvPairs(nil) != nil {
		t.Error("nil registry AllContainerEnvPairs() should return nil")
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

// writePluginYAML drops a plugin.yaml file with arbitrary contents under
// dir/<name>/plugin.yaml. Used by FindSecretOwner tests to exercise the
// real LoadAll → Registry path.
func writePluginYAML(t *testing.T, dir, name, body string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func loadRegistryFromDir(t *testing.T, userDir, projDir string) *Registry {
	t.Helper()
	ps, err := LoadAll(userDir, projDir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return NewRegistry(ps)
}

func TestFindSecretOwner_PluginLevel(t *testing.T) {
	dir := t.TempDir()
	writePluginYAML(t, dir, "github", `name: github
version: "1.0.0"
description: "d"
secrets:
  - name: github_pat
    env_var: GH_TOKEN
    description: "PAT"
    default_secret_name: github-pat
`)
	reg := loadRegistryFromDir(t, dir, "")

	p, def, ok := reg.FindSecretOwner(&config.Config{}, "github_pat")
	if !ok {
		t.Fatal("expected to find github_pat")
	}
	if p.Name() != "github" {
		t.Errorf("expected plugin github, got %q", p.Name())
	}
	if def.EnvVar != "GH_TOKEN" {
		t.Errorf("expected EnvVar=GH_TOKEN, got %q", def.EnvVar)
	}
}

func TestFindSecretOwner_AuthProviderScoped(t *testing.T) {
	dir := t.TempDir()
	writePluginYAML(t, dir, "claude", `name: claude
version: "1.0.0"
description: "d"
auth_providers:
  - name: api_key
    description: "API key"
    secrets:
      - name: claude_api_key
        env_var: ANTHROPIC_API_KEY
        default_secret_name: claude-api-key
  - name: vertex
    description: "Vertex"
    iam_roles:
      - roles/aiplatform.user
`)
	reg := loadRegistryFromDir(t, dir, "")

	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"claude": {"auth_provider": "api_key"},
		},
	}
	p, def, ok := reg.FindSecretOwner(cfg, "claude_api_key")
	if !ok {
		t.Fatal("expected to find claude_api_key under active auth_provider api_key")
	}
	if p.Name() != "claude" || def.EnvVar != "ANTHROPIC_API_KEY" {
		t.Errorf("got plugin=%q env=%q", p.Name(), def.EnvVar)
	}
}

func TestFindSecretOwner_AuthProviderInactive(t *testing.T) {
	// Same plugin, but the active auth_provider is "vertex" which has no
	// secrets — claude_api_key must not be returned.
	dir := t.TempDir()
	writePluginYAML(t, dir, "claude", `name: claude
version: "1.0.0"
description: "d"
auth_providers:
  - name: api_key
    description: "API key"
    secrets:
      - name: claude_api_key
        env_var: ANTHROPIC_API_KEY
        default_secret_name: claude-api-key
  - name: vertex
    description: "Vertex"
    iam_roles:
      - roles/aiplatform.user
`)
	reg := loadRegistryFromDir(t, dir, "")

	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"claude": {"auth_provider": "vertex"},
		},
	}
	if _, _, ok := reg.FindSecretOwner(cfg, "claude_api_key"); ok {
		t.Fatal("expected claude_api_key to be unreachable when active provider is vertex")
	}
}

func TestFindSecretOwner_Orphan(t *testing.T) {
	dir := t.TempDir()
	writePluginYAML(t, dir, "github", `name: github
version: "1.0.0"
description: "d"
secrets:
  - name: github_pat
    env_var: GH_TOKEN
    default_secret_name: github-pat
`)
	reg := loadRegistryFromDir(t, dir, "")

	if _, _, ok := reg.FindSecretOwner(&config.Config{}, "ghost_secret"); ok {
		t.Fatal("expected ok=false for orphan logical name")
	}
}

func TestFindSecretOwner_NilRegistry(t *testing.T) {
	var reg *Registry
	if _, _, ok := reg.FindSecretOwner(&config.Config{}, "anything"); ok {
		t.Fatal("nil registry must return ok=false")
	}
}

func TestPortByName(t *testing.T) {
	dir := t.TempDir()
	writePluginYAML(t, dir, "marimo", `name: marimo
version: "1.0.0"
description: "d"
port-for-tunnel: 2718
`)
	writePluginYAML(t, dir, "github", `name: github
version: "1.0.0"
description: "d"
`)
	reg := loadRegistryFromDir(t, dir, "")

	if port, ok := reg.PortByName("marimo", nil); !ok || port != 2718 {
		t.Errorf("PortByName(marimo, nil) = (%d,%v), want (2718,true)", port, ok)
	}
	if _, ok := reg.PortByName("github", nil); ok {
		t.Error("PortByName(github, nil) should be false (no port declared)")
	}
	if _, ok := reg.PortByName("nonexistent", nil); ok {
		t.Error("PortByName(nonexistent, nil) should be false")
	}

	// User-configured value wins over schema default.
	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"marimo": {"port-for-tunnel": 8000},
		},
	}
	if port, ok := reg.PortByName("marimo", cfg); !ok || port != 8000 {
		t.Errorf("PortByName(marimo, cfg) = (%d,%v), want (8000,true)", port, ok)
	}

	// A plugin without a schema default cannot be made tunnelable just by
	// dropping the key into config.yaml.
	cfg2 := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"github": {"port-for-tunnel": 8000},
		},
	}
	if _, ok := reg.PortByName("github", cfg2); ok {
		t.Error("PortByName(github, cfg2) should be false (schema has no port-for-tunnel)")
	}

	var nilReg *Registry
	if _, ok := nilReg.PortByName("anything", nil); ok {
		t.Error("nil registry PortByName should be false")
	}
}

func TestTunnelablePlugins(t *testing.T) {
	dir := t.TempDir()
	writePluginYAML(t, dir, "marimo", `name: marimo
version: "1.0.0"
description: "d"
port-for-tunnel: 2718
`)
	writePluginYAML(t, dir, "jupyter", `name: jupyter
version: "1.0.0"
description: "d"
port-for-tunnel: 8888
`)
	writePluginYAML(t, dir, "github", `name: github
version: "1.0.0"
description: "d"
`)
	reg := loadRegistryFromDir(t, dir, "")

	got := reg.TunnelablePlugins()
	want := []string{"jupyter", "marimo"} // alphabetical
	if len(got) != len(want) {
		t.Fatalf("TunnelablePlugins() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("TunnelablePlugins()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	var nilReg *Registry
	if nilReg.TunnelablePlugins() != nil {
		t.Error("nil registry TunnelablePlugins should be nil")
	}
}

func TestFindSecretOwner_ValueCommandPreserved(t *testing.T) {
	// The codex case: the returned SecretDef must carry the value_command
	// so callers can dispatch through CollectSecretValue.
	dir := t.TempDir()
	writePluginYAML(t, dir, "codex", `name: codex
version: "1.0.0"
description: "d"
auth_providers:
  - name: oauth
    description: "OAuth"
    secrets:
      - name: codex_auth_json
        env_var: CODEX_AUTH_JSON_B64
        default_secret_name: codex-auth-json
        value_command:
          command: codex
          args: ["login"]
          output: file_base64
          path: "~/.codex/auth.json"
          validate: json
          interactive: true
`)
	reg := loadRegistryFromDir(t, dir, "")

	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"codex": {"auth_provider": "oauth"},
		},
	}
	_, def, ok := reg.FindSecretOwner(cfg, "codex_auth_json")
	if !ok {
		t.Fatal("expected to find codex_auth_json")
	}
	if def.ValueCommand == nil {
		t.Fatal("expected ValueCommand to be preserved")
	}
	if def.ValueCommand.Command != "codex" || def.ValueCommand.Output != "file_base64" {
		t.Errorf("unexpected value_command: %+v", def.ValueCommand)
	}
}
