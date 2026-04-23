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
	proj, err := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(proj), "project_id: my-proj-123") {
		t.Errorf(".ws/config.yaml missing project_id:\n%s", proj)
	}
	if !strings.Contains(string(proj), "github_repo_url: https://github.com/a/b.git") {
		t.Errorf(".ws/config.yaml missing github_repo_url:\n%s", proj)
	}

	// Global file
	global, err := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(global), "region: us-central1") {
		t.Errorf("global config missing region:\n%s", global)
	}
	if !strings.Contains(string(global), `git_user_name:  "Alice"`) {
		t.Errorf("global config missing git_user_name:\n%s", global)
	}
}

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

func TestInteractiveScaffold_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ws", "config.yaml"), []byte("existing: true\n"), 0644); err != nil {
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
	got, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
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
	proj, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	if !strings.Contains(string(proj), "probed: probed-proj") {
		t.Errorf("template missing probed project id:\n%s", proj)
	}
	global, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if !strings.Contains(string(global), "europe-west1") {
		t.Errorf("global template missing region:\n%s", global)
	}
}

func TestInteractiveScaffold_WritesAllKeysInTemplateForm(t *testing.T) {
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
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	proj, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	// User-set keys: uncommented.
	for _, want := range []string{
		"project_id: my-proj-123",
		"github_repo_url: https://github.com/a/b.git",
	} {
		if !strings.Contains(string(proj), want) {
			t.Errorf("project config missing %q\ngot:\n%s", want, proj)
		}
	}
	// Optional keys not prompted (no --advanced): commented.
	for _, want := range []string{
		"# region:",
		"# cluster_name:",
		"# config_name:",
		"# machine_type:",
		"# disk_size_gb:",
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
	} {
		if !strings.Contains(string(proj), want) {
			t.Errorf("project config missing commented %q\ngot:\n%s", want, proj)
		}
	}

	global, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	// User-set keys under defaults:
	for _, want := range []string{
		"  region: us-central1",
		`  git_user_name:  "Alice"`,
		`  git_user_email: "alice@example.com"`,
	} {
		if !strings.Contains(string(global), want) {
			t.Errorf("global config missing %q\ngot:\n%s", want, global)
		}
	}
	// Not-prompted keys: commented.
	for _, want := range []string{
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
		"# projects:",
	} {
		if !strings.Contains(string(global), want) {
			t.Errorf("global config missing commented %q\ngot:\n%s", want, global)
		}
	}
}

func TestInteractiveScaffold_AdvancedUncommentsOptionalProjectKeys(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// --advanced: project_id, github_repo_url, region, cluster_name,
	// config_name, machine_type, disk_size_gb, then global region,
	// git_user_name, git_user_email, then confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-east1",
		"my-cluster",
		"my-config",
		"e2-standard-8",
		"100",
		"us-east1",
		"Alice",
		"alice@example.com",
		"y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{}, Advanced: true,
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	proj, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	for _, want := range []string{
		"region: us-east1",
		"cluster_name: my-cluster",
		"config_name: my-config",
		"machine_type: e2-standard-8",
		"disk_size_gb: 100",
	} {
		if !strings.Contains(string(proj), want) {
			t.Errorf("project config missing uncommented %q\ngot:\n%s", want, proj)
		}
	}
	// auto_sleep_hours / auto_shutdown_hours are never prompted, even with
	// --advanced — they must stay commented.
	for _, want := range []string{"# auto_sleep_hours:", "# auto_shutdown_hours:"} {
		if !strings.Contains(string(proj), want) {
			t.Errorf("project config missing commented %q in --advanced run\ngot:\n%s", want, proj)
		}
	}
	global, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if !strings.Contains(string(global), `git_user_name:  "Alice"`) {
		t.Errorf("global config missing git_user_name in --advanced run:\n%s", global)
	}
}
