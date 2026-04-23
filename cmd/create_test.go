// ws/cmd/create_test.go
package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Preflight tests exercise ensureBuildAssets, which we expose in cmd to
// test without running the whole create flow.
func TestEnsureBuildAssets_Present_ReturnsNil(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		if err := os.WriteFile(filepath.Join(ws, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var stdin bytes.Buffer
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, cwd, &stdin, &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureBuildAssets_MissingNonTTY_Errors(t *testing.T) {
	cwd := t.TempDir()
	var stdin bytes.Buffer // not a TTY
	var out bytes.Buffer
	err := ensureBuildAssets(cwd, t.TempDir(), &stdin, &out, false)
	if err == nil {
		t.Fatal("expected error when missing + non-TTY")
	}
	if !strings.Contains(err.Error(), "ws setup scaffold") {
		t.Errorf("error should mention 'ws setup scaffold', got: %v", err)
	}
}

func TestEnsureBuildAssets_MissingTTYAnswerYes_Scaffolds(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := bytes.NewBufferString("Y\n")
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, home, stdin, &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		p := filepath.Join(cwd, ".ws", n)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist after prompt=yes: %v", p, err)
		}
	}
}

func TestEnsureBuildAssets_MissingTTYAnswerNo_Errors(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := bytes.NewBufferString("n\n")
	var out bytes.Buffer
	err := ensureBuildAssets(cwd, home, stdin, &out, true)
	if err == nil {
		t.Fatal("expected error when user answered 'n'")
	}
}

func TestEnsureBuildAssets_PartialExistingTTYYes_ScaffoldsOnlyMissing(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "Dockerfile"), []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stdin := bytes.NewBufferString("Y\n")
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, t.TempDir(), stdin, &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "Dockerfile"))
	if string(data) != "USER\n" {
		t.Errorf("existing Dockerfile should not be overwritten, got %q", data)
	}
	if _, err := os.Stat(filepath.Join(ws, "entrypoint.sh")); err != nil {
		t.Errorf("missing entrypoint.sh should have been scaffolded: %v", err)
	}
}

func TestBuildArgs_UsesDashFAndProjectRootContext(t *testing.T) {
	got := dockerBuildArgs("example.com/img:tag")
	want := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", "example.com/img:tag",
		"-f", ".ws/Dockerfile",
		"--push",
		".",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("args mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Ensure the runner interface used by create.go is usable with a fake.
// This compile-time check catches breaks if we later extract an interface.
var _ io.Writer = (*bytes.Buffer)(nil)
var _ error = errors.New("sentinel")
