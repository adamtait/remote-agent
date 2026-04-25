// ws/cmd/scaffold_test.go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cmd"
)

func TestSetupScaffold_WritesEmbeddedDefaults(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		p := filepath.Join(cwd, ".ra", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestSetupScaffold_RefusesExistingWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	if err := os.MkdirAll(filepath.Join(cwd, ".ra"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ra", "Dockerfile"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold"})
	if code == 0 {
		t.Fatalf("expected non-zero exit, stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("stderr should mention 'already exists': %q", stderr.String())
	}
}

func TestSetupScaffold_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	if err := os.MkdirAll(filepath.Join(cwd, ".ra"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ra", "Dockerfile"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold", "--force"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}

	data, _ := os.ReadFile(filepath.Join(cwd, ".ra", "Dockerfile"))
	if string(data) == "old\n" {
		t.Errorf("--force did not overwrite")
	}
}

func TestSetupScaffold_DryRun_WritesNothing(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold", "--dry-run"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(cwd, ".ra", "Dockerfile")); !os.IsNotExist(err) {
		t.Error("dry-run should not create .ra/Dockerfile")
	}
	if !strings.Contains(stdout.String(), "would write") {
		t.Errorf("expected 'would write' in stdout, got %q", stdout.String())
	}
}
