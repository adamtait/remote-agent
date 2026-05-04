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

func TestInteractiveScaffold_WritesExhaustiveUserConfig(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"Alice",
		"alice@example.com",
		"y", // confirm
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	user, _ := os.ReadFile(filepath.Join(home, ".config", "ra", "config.yaml"))
	for _, want := range []string{
		`  user_name: "Alice"`,
		`  user_email: "alice@example.com"`,
		`  project: my-proj-123`,
		`    region: us-central1`,
		`    machine_type: e2-standard-4`,
		`secrets: {}`,
		`env: {}`,
	} {
		if !strings.Contains(string(user), want) {
			t.Errorf("user config missing %q\n---\n%s", want, user)
		}
	}

	project, _ := os.ReadFile(filepath.Join(cwd, ".ra", "config.yaml"))
	for _, want := range []string{
		`  project: my-proj-123`,
		`#   workstation:`,
		`# gcloud:`,
		`# secrets:`,
	} {
		if !strings.Contains(string(project), want) {
			t.Errorf("project config missing %q\n---\n%s", want, project)
		}
	}

	// User-dir assets exist (written to ~/.config/ra/).
	for _, name := range []string{"Dockerfile"} {
		userAsset := filepath.Join(home, ".config", "ra", name)
		if _, err := os.Stat(userAsset); err != nil {
			t.Errorf("user asset %s not materialized: %v", name, err)
		}
	}
	// Project-local assets do NOT exist without --with-project-assets.
	projectAsset := filepath.Join(cwd, ".ra", "Dockerfile")
	if _, err := os.Stat(projectAsset); err == nil {
		t.Errorf("project asset Dockerfile should NOT exist without --with-project-assets")
	}
}

func TestInteractiveScaffold_WithProjectAssetsCopiesAssetsToDotRA(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
		WithProjectAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Dockerfile", "workstation-startup.d/200_remote-agent-setup.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ra", name)); err != nil {
			t.Errorf(".ra/%s missing despite --with-project-assets: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".config", "ra", name)); err != nil {
			t.Errorf("user config dir %s missing: %v", name, err)
		}
	}
}

func TestInteractiveScaffold_IdempotentWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userCfg := filepath.Join(home, ".config", "ra", "config.yaml")
	projectCfg := filepath.Join(cwd, ".ra", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(projectCfg), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(userCfg, []byte("preexisting: user\n"), 0644)
	os.WriteFile(projectCfg, []byte("preexisting: project\n"), 0644)

	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no-op success, got %v", err)
	}
	for _, p := range []string{userCfg, projectCfg} {
		got, _ := os.ReadFile(p)
		if !strings.HasPrefix(string(got), "preexisting:") {
			t.Errorf("%s was overwritten without --force:\n%s", p, got)
		}
	}
}

func TestInteractiveScaffold_ProjectExists_SkipsAndPreserves(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ra"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ra", "config.yaml"), []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Project exists → user config still prompts for project_id,
	// then git_user_name, git_user_email, confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no error for pre-existing project config, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".ra", "config.yaml"))
	if string(got) != "existing: true\n" {
		t.Errorf("existing project file modified: %q", got)
	}
	// Core assets materialized in user config dir.
	if _, err := os.Stat(filepath.Join(home, ".config", "ra", "Dockerfile")); err != nil {
		t.Errorf("Dockerfile not materialized in user config dir: %v", err)
	}
}

func TestInteractiveScaffold_BothConfigsExist_IsNoOp(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ra"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ra", "config.yaml"), []byte("existing: project\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "ra"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "ra", "config.yaml"), []byte("existing: global\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Empty stdin: no prompts consumed, nothing written.
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no error when both configs exist, got: %v", err)
	}
	proj, _ := os.ReadFile(filepath.Join(cwd, ".ra", "config.yaml"))
	if string(proj) != "existing: project\n" {
		t.Errorf("project config modified: %q", proj)
	}
	global, _ := os.ReadFile(filepath.Join(home, ".config", "ra", "config.yaml"))
	if string(global) != "existing: global\n" {
		t.Errorf("global config modified: %q", global)
	}
	// Assets still materialized in user config dir.
	for _, name := range []string{"Dockerfile"} {
		if _, err := os.Stat(filepath.Join(home, ".config", "ra", name)); err != nil {
			t.Errorf("%s not materialized on re-run: %v", name, err)
		}
	}
}

func TestInteractiveScaffold_ProjectOnlySkipsGlobal(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
		ProjectOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".ra", "config.yaml")); err != nil {
		t.Errorf("project file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "ra", "config.yaml")); !errors.Is(err, os.ErrNotExist) {
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
	// Full run prompts: project_id, git_user_name,
	// git_user_email, then "no" to cancel.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
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
	if _, statErr := os.Stat(filepath.Join(cwd, ".ra", "config.yaml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("project file should not exist after cancel")
	}
}

func TestInteractiveScaffold_DeclineWrapsErrCancelled(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
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
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
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

func TestInteractiveScaffold_GlobalExists_SkipsAndScaffoldsAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	globalDir := filepath.Join(home, ".config", "ra")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("defaults:\n  region: us-east1\n")
	if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), original, 0644); err != nil {
		t.Fatal(err)
	}
	// User config exists → only project prompts: project_id, confirm
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "y",
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
	if _, statErr := os.Stat(filepath.Join(home, ".config", "ra", "Dockerfile")); statErr != nil {
		t.Errorf("Dockerfile not materialized in user config dir: %v", statErr)
	}
}

func TestTemplateScaffold_GlobalExists_SkipsAndScaffoldsAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	globalDir := filepath.Join(home, ".config", "ra")
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
	if _, statErr := os.Stat(filepath.Join(home, ".config", "ra", "Dockerfile")); statErr != nil {
		t.Errorf("Dockerfile not materialized in user config dir: %v", statErr)
	}
}
