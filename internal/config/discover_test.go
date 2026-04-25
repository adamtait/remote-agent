// ra/internal/config/discover_test.go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ra"), 0755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".ra", "config.yaml")
	if err := os.WriteFile(p, []byte("project_id: test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindProjectConfig_FoundInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	want := writeConfig(t, dir)
	got, err := config.FindProjectConfig(dir, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindProjectConfig_FoundInParent(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	want := writeConfig(t, root)
	got, err := config.FindProjectConfig(sub, filepath.Dir(root))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindProjectConfig_StopsAtHome(t *testing.T) {
	home := t.TempDir()
	sub := filepath.Join(home, "project", "src")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := config.FindProjectConfig(sub, home)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindProjectConfig_NotFoundReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := config.FindProjectConfig(dir, filepath.Dir(dir))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
