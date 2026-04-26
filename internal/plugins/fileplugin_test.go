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
