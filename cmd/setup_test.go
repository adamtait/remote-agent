// ra/cmd/setup_test.go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cmd"
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

	projectPath := filepath.Join(cwd, ".ra", "config.yaml")
	if _, err := os.Stat(projectPath); err != nil {
		t.Fatalf("expected %s to exist: %v", projectPath, err)
	}
	globalPath := filepath.Join(home, ".config", "ra", "config.yaml")
	if _, err := os.Stat(globalPath); err != nil {
		t.Fatalf("expected %s to exist: %v", globalPath, err)
	}
}

func TestSetupTemplate_ExistingSkipsAndPreservesContent(t *testing.T) {
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
	projectPath := filepath.Join(cwd, ".ra", "config.yaml")
	original := []byte("old: content\n")
	if err := os.WriteFile(projectPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdin bytes.Buffer
	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template"})
	if code != 0 {
		t.Fatalf("expected exit 0 on re-run, got %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "skip") || !strings.Contains(stdout.String(), "already present") {
		t.Errorf("expected skip notice in stdout, got: %q", stdout.String())
	}
	got, _ := os.ReadFile(projectPath)
	if !bytes.Equal(got, original) {
		t.Errorf("existing project config modified: %q", got)
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

	if err := os.MkdirAll(filepath.Join(cwd, ".ra"), 0755); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(cwd, ".ra", "config.yaml")
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

func TestSetupTemplate_WritesAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		p := filepath.Join(home, ".config", "ra", "template", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestSetupTemplate_GlobalOnly_SkipsAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template", "--global-only"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, ".ra", "Dockerfile")); !os.IsNotExist(err) {
		t.Error("--global-only should not write .ra/Dockerfile")
	}
}

func TestSetupTemplate_ProjectOnly_WritesAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template", "--project-only"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(home, ".config", "ra", "template", name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// Global config should not exist under --project-only.
	if _, err := os.Stat(filepath.Join(home, ".config", "ra", "config.yaml")); !os.IsNotExist(err) {
		t.Error("--project-only should not write ~/.config/ra/config.yaml")
	}
}

func TestSetupInteractive_AlwaysScaffoldsAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	// Pipe pre-canned answers for every prompt in order:
	//   project_id, github_repo_url, auth_mode, git_user_name, git_user_email,
	//   Proceed? [Y/n]
	input := strings.Join([]string{
		"my-project",
		"https://github.com/a/b.git",
		"1", // oauth
		"Alice",
		"a@example.com",
		"Y", // Proceed?
		"",  // trailing newline
	}, "\n")
	var stdin bytes.Buffer
	stdin.WriteString(input)

	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	// config.yaml still in .ws/
	if _, err := os.Stat(filepath.Join(cwd, ".ra", "config.yaml")); err != nil {
		t.Errorf("missing .ra/config.yaml: %v", err)
	}
	// assets in user template dir
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(home, ".config", "ra", "template", name)); err != nil {
			t.Errorf("missing user template %s: %v", name, err)
		}
	}
}

func TestSetupInteractive_RerunSkipsExistingAssets(t *testing.T) {
	// Re-running ra setup must not error on pre-existing scaffold assets;
	// OnlyMissing semantics make the scaffold step idempotent.
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	// Pre-create a customized Dockerfile in user template dir that must survive setup.
	userTemplateDir := filepath.Join(home, ".config", "ra", "template")
	if err := os.MkdirAll(userTemplateDir, 0755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("# user customization\n")
	if err := os.WriteFile(filepath.Join(userTemplateDir, "Dockerfile"), custom, 0644); err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		"my-project", "https://github.com/a/b.git", "1",
		"Alice", "a@example.com", "Y", "",
	}, "\n")
	var stdin bytes.Buffer
	stdin.WriteString(input)
	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	got, _ := os.ReadFile(filepath.Join(userTemplateDir, "Dockerfile"))
	if string(got) != string(custom) {
		t.Errorf("custom Dockerfile was overwritten:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(userTemplateDir, "entrypoint.sh")); err != nil {
		t.Errorf("entrypoint.sh should still be scaffolded: %v", err)
	}
}
