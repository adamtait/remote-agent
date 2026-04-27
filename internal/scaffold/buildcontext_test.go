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

func TestAssembleBuildContext_DockerfileFromEmbedded(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatalf("AssembleBuildContext: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatalf("reading Dockerfile: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("Dockerfile is empty; expected embedded default bytes")
	}
}

func TestAssembleBuildContext_DockerfileFromUser(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userPath := filepath.Join(home, ".config", "ra", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(userPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte("USER-DOCKERFILE\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "USER-DOCKERFILE\n" {
		t.Fatalf("Dockerfile = %q, want USER-DOCKERFILE", got)
	}
}

func TestAssembleBuildContext_DockerfileFromProject(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userPath := filepath.Join(home, ".config", "ra", "Dockerfile")
	projPath := filepath.Join(cwd, ".ra", "Dockerfile")
	for _, p := range []string{userPath, projPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(userPath, []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projPath, []byte("PROJECT\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PROJECT\n" {
		t.Fatalf("Dockerfile = %q, want PROJECT", got)
	}
}
