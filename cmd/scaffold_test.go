// ws/cmd/scaffold_test.go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
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

	for _, name := range []string{"Dockerfile", "workstation-startup.d/200_remote-agent-setup.sh"} {
		p := filepath.Join(cwd, ".ra", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestSetupScaffold_SkipsExistingWithoutForce(t *testing.T) {
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
	original := []byte("old\n")
	if err := os.WriteFile(filepath.Join(cwd, ".ra", "Dockerfile"), original, 0644); err != nil {
		t.Fatal(err)
	}

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold"})
	if code != 0 {
		t.Fatalf("expected exit 0 (skip semantics), stderr=%q", stderr.String())
	}

	data, _ := os.ReadFile(filepath.Join(cwd, ".ra", "Dockerfile"))
	if !bytes.Equal(data, original) {
		t.Errorf("existing file was overwritten without --force: %q", data)
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
