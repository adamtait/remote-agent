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

func TestAssembleBuildContext_MergesProfileD(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// User-global has a.sh; project has a.sh (override) and b.sh
	mustWrite(t, filepath.Join(home, ".config", "ra", "profile.d", "a.sh"), "USER-A")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "a.sh"), "PROJ-A")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "b.sh"), "PROJ-B")

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	a, _ := os.ReadFile(filepath.Join(path, "profile.d", "a.sh"))
	b, _ := os.ReadFile(filepath.Join(path, "profile.d", "b.sh"))
	if string(a) != "PROJ-A" {
		t.Errorf("a.sh = %q, want PROJ-A (project shadows user)", a)
	}
	if string(b) != "PROJ-B" {
		t.Errorf("b.sh = %q, want PROJ-B", b)
	}
}

func TestAssembleBuildContext_EmptyProfileDGetsKeep(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	for _, dir := range []string{"profile.d", "workstation-startup.d", "Dockerfile.d"} {
		if _, err := os.Stat(filepath.Join(path, dir, ".ra-keep")); err != nil {
			t.Errorf("%s/.ra-keep missing: %v", dir, err)
		}
	}
}

// mustWrite is a test helper that writes content with parent-dir mkdir.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
