package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssembleBuildContext_CreatesBuildDir(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{
		Cwd:  cwd,
		Home: home,
	})
	if err != nil {
		t.Fatalf("AssembleBuildContext: %v", err)
	}
	defer cleanup()

	want := filepath.Join(cwd, ".ra", ".build")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected build dir to exist: %v", err)
	}
}
