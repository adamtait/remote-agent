// ws/internal/scaffold/scaffold_test.go
package scaffold_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

// TestInteractiveScaffold_WritesBothFiles: removed — Task 8 will rewrite
// InteractiveScaffold with new render functions and restore content checks.

func TestInteractiveScaffold_ProjectExists_SkipsAndPreserves(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ws", "config.yaml"), []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Only global prompts should be consumed: region, git_user_name,
	// git_user_email, then Proceed? confirm. Project prompts are skipped
	// because .ws/config.yaml already exists.
	stdin := strings.NewReader(strings.Join([]string{
		"us-central1", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no error for pre-existing project config, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	if string(got) != "existing: true\n" {
		t.Errorf("existing project file modified: %q", got)
	}
	// Assets still scaffolded.
	if _, err := os.Stat(filepath.Join(cwd, ".ws", "Dockerfile")); err != nil {
		t.Errorf("Dockerfile not scaffolded: %v", err)
	}
}

func TestInteractiveScaffold_BothConfigsExist_IsNoOp(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ws", "config.yaml"), []byte("existing: project\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "ws", "config.yaml"), []byte("existing: global\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Empty stdin: no prompts should be consumed because nothing will be written.
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no error when both configs exist, got: %v", err)
	}
	// Both config files preserved byte-for-byte.
	proj, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	if string(proj) != "existing: project\n" {
		t.Errorf("project config modified: %q", proj)
	}
	global, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if string(global) != "existing: global\n" {
		t.Errorf("global config modified: %q", global)
	}
	// Assets still scaffolded.
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ws", name)); err != nil {
			t.Errorf("%s not scaffolded on re-run: %v", name, err)
		}
	}
}

// TestInteractiveScaffold_ForceOverwrites: removed — Task 8 will rewrite
// InteractiveScaffold with new render functions and restore content checks.

func TestInteractiveScaffold_ProjectOnlySkipsGlobal(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{}, ProjectOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".ws", "config.yaml")); err != nil {
		t.Errorf("project file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "ws", "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("global file was written despite --project-only")
	}
}

func TestInteractiveScaffold_MutuallyExclusiveFlags(t *testing.T) {
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Cwd: t.TempDir(), Home: t.TempDir(), Prober: &scaffold.StaticProber{},
		ProjectOnly: true, GlobalOnly: true,
	})
	if err == nil {
		t.Fatal("expected error for mutually exclusive flags")
	}
}

func TestInteractiveScaffold_NoCancelsOnProceed(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-central1",
		"Alice",
		"alice@example.com",
		"no",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(cwd, ".ws", "config.yaml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("project file should not exist after cancel")
	}
}

func TestInteractiveScaffold_DeclineWrapsErrCancelled(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-central1",
		"Alice",
		"alice@example.com",
		"no",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, scaffold.ErrCancelled) {
		t.Errorf("expected errors.Is(err, ErrCancelled), got: %v", err)
	}
}

func TestInteractiveScaffold_NilStderrDoesNotPanic(t *testing.T) {
	// Guard against a future caller forgetting to pipe Stderr.
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-central1",
		"Alice",
		"alice@example.com",
		"y", // Proceed?
	}, "\n") + "\n")
	var stdout bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: nil,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("unexpected error with nil Stderr: %v", err)
	}
}

func TestInteractiveScaffold_GlobalExists_SkipsAndScaffoldsAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	globalDir := filepath.Join(home, ".config", "ws")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("defaults:\n  region: us-east1\n")
	if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), original, 0644); err != nil {
		t.Fatal(err)
	}
	// Only project prompts consumed: project_id, github_repo_url, confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no error for pre-existing global config, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(globalDir, "config.yaml"))
	if !bytes.Equal(got, original) {
		t.Errorf("existing global config modified: %q", got)
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, statErr := os.Stat(filepath.Join(cwd, ".ws", name)); statErr != nil {
			t.Errorf("%s not scaffolded: %v", name, statErr)
		}
	}
}

func TestTemplateScaffold_GlobalExists_SkipsAndScaffoldsAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	globalDir := filepath.Join(home, ".config", "ws")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("defaults:\n  region: us-east1\n")
	if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), original, 0644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := scaffold.TemplateScaffold(scaffold.TemplateOptions{
		Stdout: &stdout, Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no error for pre-existing global config, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(globalDir, "config.yaml"))
	if !bytes.Equal(got, original) {
		t.Errorf("existing global config modified: %q", got)
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, statErr := os.Stat(filepath.Join(cwd, ".ws", name)); statErr != nil {
			t.Errorf("%s not scaffolded: %v", name, statErr)
		}
	}
}

// TestTemplateScaffold_WritesBothFiles: removed — Task 8 will rewrite
// TemplateScaffold with new render functions and restore content checks.

// TestInteractiveScaffold_WritesAllKeysInTemplateForm: removed — Task 8 will
// rewrite InteractiveScaffold with new render functions and restore content checks.

// TestInteractiveScaffold_AdvancedUncommentsOptionalProjectKeys: removed — Task 8
// will rewrite InteractiveScaffold with new render functions and restore content checks.
