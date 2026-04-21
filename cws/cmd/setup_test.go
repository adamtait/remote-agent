// cws/cmd/setup_test.go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/cmd"
)

func TestSetupTemplate_WritesFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin bytes.Buffer
	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	projectPath := filepath.Join(cwd, ".workstation.yaml")
	if _, err := os.Stat(projectPath); err != nil {
		t.Fatalf("expected %s to exist: %v", projectPath, err)
	}
	globalPath := filepath.Join(home, ".config", "cws", "config.yaml")
	if _, err := os.Stat(globalPath); err != nil {
		t.Fatalf("expected %s to exist: %v", globalPath, err)
	}
}

func TestSetupTemplate_RefusesExistingWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	projectPath := filepath.Join(cwd, ".workstation.yaml")
	if err := os.WriteFile(projectPath, []byte("old: content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdin bytes.Buffer
	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template"})
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0. stderr=%q stdout=%q", stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("expected stderr to contain %q, got: %q", "already exists", stderr.String())
	}
}

func TestSetupTemplate_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	projectPath := filepath.Join(cwd, ".workstation.yaml")
	if err := os.WriteFile(projectPath, []byte("old: content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdin bytes.Buffer
	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template", "--force"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	data, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old") {
		t.Fatalf("expected %s to be overwritten, still contains 'old': %q", projectPath, string(data))
	}
}
