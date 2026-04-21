// cws/internal/config/discover_test.go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/config"
)

func TestFindProjectConfig_FoundInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".workstation.yaml")
	if err := os.WriteFile(cfgPath, []byte("project_id: test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := config.FindProjectConfig(dir, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfgPath {
		t.Errorf("got %q, want %q", got, cfgPath)
	}
}

func TestFindProjectConfig_FoundInParent(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, ".workstation.yaml")
	if err := os.WriteFile(cfgPath, []byte("project_id: test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := config.FindProjectConfig(sub, filepath.Dir(root))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfgPath {
		t.Errorf("got %q, want %q", got, cfgPath)
	}
}

func TestFindProjectConfig_StopsAtHome(t *testing.T) {
	home := t.TempDir()
	sub := filepath.Join(home, "project", "src")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	// no .workstation.yaml anywhere
	_, err := config.FindProjectConfig(sub, home)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindProjectConfig_NotFoundReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := config.FindProjectConfig(dir, filepath.Dir(dir))
	if err == nil {
		t.Fatal("expected error for missing .workstation.yaml, got nil")
	}
}
