package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

// writePlugin is a helper that creates a minimal plugin.yaml under dir/<name>/plugin.yaml.
func writePlugin(t *testing.T, dir, name, version string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: " + name + "\nversion: \"" + version + "\"\ndescription: \"d\"\n"
	if err := os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAll_EmptyDirs(t *testing.T) {
	// With no dirs and no embedded defaults, LoadAll should return an empty list.
	plugins, err := LoadAll("", "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("expected no plugins, got %d: %v", len(plugins), plugins)
	}
}

func TestLoadAll_UserDir(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "myplugin", "1.0.0")

	plugins, err := LoadAll(dir, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(plugins) != 1 || plugins[0].Name() != "myplugin" {
		t.Errorf("expected [myplugin], got %v", plugins)
	}
}

func TestLoadAll_ProjectShadowsUser(t *testing.T) {
	userDir := t.TempDir()
	projDir := t.TempDir()

	writePlugin(t, userDir, "myplugin", "1.0.0")
	writePlugin(t, projDir, "myplugin", "2.0.0")

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
