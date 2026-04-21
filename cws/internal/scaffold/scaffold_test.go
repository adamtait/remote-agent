// cws/internal/scaffold/scaffold_test.go
package scaffold_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestInteractiveScaffold_WritesBothFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// Inputs (in order): project_id, github_repo_url, region, git_user_name,
	// git_user_email, then "y" to confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-central1",
		"Alice",
		"alice@example.com",
		"y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	prober := &scaffold.StaticProber{HasGcloud_: true, HasAuth_: true, HasDocker_: true}

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: prober,
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	// Project file
	proj, err := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(proj), "project_id: my-proj-123") {
		t.Errorf(".workstation.yaml missing project_id:\n%s", proj)
	}
	if !strings.Contains(string(proj), "github_repo_url: https://github.com/a/b.git") {
		t.Errorf(".workstation.yaml missing github_repo_url:\n%s", proj)
	}

	// Global file
	global, err := os.ReadFile(filepath.Join(home, ".config", "cws", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(global), "region: us-central1") {
		t.Errorf("global config missing region:\n%s", global)
	}
	if !strings.Contains(string(global), "git_user_name: Alice") {
		t.Errorf("global config missing git_user_name:\n%s", global)
	}
}

func TestInteractiveScaffold_RefusesIfProjectFileExists(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".workstation.yaml"), []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "us-central1", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// File unchanged
	got, _ := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if string(got) != "existing: true\n" {
		t.Errorf("file modified: %q", got)
	}
}

func TestInteractiveScaffold_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".workstation.yaml"), []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "us-central1", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{}, Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if !strings.Contains(string(got), "my-proj-123") {
		t.Errorf("file not overwritten: %q", got)
	}
}

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
	if _, err := os.Stat(filepath.Join(cwd, ".workstation.yaml")); err != nil {
		t.Errorf("project file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "cws", "config.yaml")); !errors.Is(err, os.ErrNotExist) {
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
	if _, statErr := os.Stat(filepath.Join(cwd, ".workstation.yaml")); !errors.Is(statErr, os.ErrNotExist) {
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
		"y",
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

func TestTemplateScaffold_WritesBothFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	var stdout bytes.Buffer

	prober := &scaffold.StaticProber{
		Project: "probed-proj", Region: "europe-west1",
		GitUserName_: "Bob", GitUserEmail_: "bob@example.com",
	}

	err := scaffold.TemplateScaffold(scaffold.TemplateOptions{
		Stdout: &stdout, Cwd: cwd, Home: home, Prober: prober,
	})
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if !strings.Contains(string(proj), "probed: probed-proj") {
		t.Errorf("template missing probed project id:\n%s", proj)
	}
	global, _ := os.ReadFile(filepath.Join(home, ".config", "cws", "config.yaml"))
	if !strings.Contains(string(global), "europe-west1") {
		t.Errorf("global template missing region:\n%s", global)
	}
}
