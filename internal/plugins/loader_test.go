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
