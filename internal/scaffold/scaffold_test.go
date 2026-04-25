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

// New prompt order (full run): project_id, github_repo_url, git_user_name,
// git_user_email, enable_claude, (if enabled) auth_mode, confirm.

func TestInteractiveScaffold_WritesExhaustiveUserConfig(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"Alice",
		"alice@example.com",
		"y",   // enable claude
		"1",   // oauth
		"y",   // confirm
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	user, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	for _, want := range []string{
		`  user_name: "Alice"`,
		`  user_email: "alice@example.com"`,
		`  github_repo_url: https://github.com/a/b.git`,
		`  project: my-proj-123`,
		`    region: us-central1`,
		`    machine_type: e2-standard-4`,
		`  claude_oauth:`,
		`    secret_name: claude-code-oauth-token`,
		`  github_pat:`,
		`env: {}`,
	} {
		if !strings.Contains(string(user), want) {
			t.Errorf("user config missing %q\n---\n%s", want, user)
		}
	}

	project, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	for _, want := range []string{
		`  github_repo_url: https://github.com/a/b.git`,
		`  project: my-proj-123`,
		`#   workstation:`,
		`# gcloud:`,
		`# secrets:`,
	} {
		if !strings.Contains(string(project), want) {
			t.Errorf("project config missing %q\n---\n%s", want, project)
		}
	}

	// User-dir assets exist; .ws/ assets do NOT (default — no --with-project-assets).
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		userAsset := filepath.Join(home, ".config", "ws", "template", name)
		if _, err := os.Stat(userAsset); err != nil {
			t.Errorf("user asset %s not materialized: %v", userAsset, err)
		}
		projectAsset := filepath.Join(cwd, ".ws", name)
		if _, err := os.Stat(projectAsset); err == nil {
			t.Errorf("project asset %s should NOT exist without --with-project-assets", projectAsset)
		}
	}
}

func TestInteractiveScaffold_ClaudeDisabled_SkipsAuthModeAndOmitsClaudeSecrets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	// After git email we answer "n" to claude_enabled — promptClaudeAuthMode
	// must NOT run, so fewer lines are consumed than in the enabled case.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"Alice",
		"alice@example.com",
		"n",
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

	user, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if !strings.Contains(string(user), "claude:\n  enabled: false") {
		t.Errorf("expected claude.enabled: false block in user config:\n%s", user)
	}
	for _, forbidden := range []string{"claude_oauth:", "claude_apikey:", "auth_mode:"} {
		if strings.Contains(string(user), forbidden) {
			t.Errorf("disabled-claude user config should not contain %q:\n%s", forbidden, user)
		}
	}
}

func TestInteractiveScaffold_APIKeyMode(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"Alice",
		"alice@example.com",
		"y",   // enable claude
		"2",   // apikey
		"y",   // confirm
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatal(err)
	}
	user, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if !strings.Contains(string(user), "claude_apikey:") {
		t.Errorf("expected claude_apikey entry in apikey mode:\n%s", user)
	}
	if strings.Contains(string(user), "claude_oauth:") {
		t.Errorf("unexpected claude_oauth entry in apikey mode:\n%s", user)
	}
}

func TestInteractiveScaffold_WithProjectAssetsCopiesAssetsToDotWS(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "Alice", "alice@example.com", "y", "1", "y",
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
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ws", name)); err != nil {
			t.Errorf(".ws/%s missing despite --with-project-assets: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".config", "ws", "template", name)); err != nil {
			t.Errorf("user template %s missing: %v", name, err)
		}
	}
}

func TestInteractiveScaffold_IdempotentWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userCfg := filepath.Join(home, ".config", "ws", "config.yaml")
	projectCfg := filepath.Join(cwd, ".ws", "config.yaml")
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
	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ws", "config.yaml"), []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Project exists → user config still prompts for project_id and github_repo_url,
	// then git_user_name, git_user_email, enable_claude, auth_mode, confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "Alice", "alice@example.com", "y", "1", "y",
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
	// User template assets materialized.
	if _, err := os.Stat(filepath.Join(home, ".config", "ws", "template", "Dockerfile")); err != nil {
		t.Errorf("Dockerfile not materialized in user template dir: %v", err)
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
	// Empty stdin: no prompts consumed, nothing written.
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no error when both configs exist, got: %v", err)
	}
	proj, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	if string(proj) != "existing: project\n" {
		t.Errorf("project config modified: %q", proj)
	}
	global, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if string(global) != "existing: global\n" {
		t.Errorf("global config modified: %q", global)
	}
	// Assets still materialized in user template dir.
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(home, ".config", "ws", "template", name)); err != nil {
			t.Errorf("%s not materialized on re-run: %v", name, err)
		}
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
	// Full run prompts: project_id, github_repo_url, git_user_name,
	// git_user_email, enable_claude, auth_mode (if enabled), no (cancel).
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"Alice",
		"alice@example.com",
		"y",
		"1",
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
		"Alice",
		"alice@example.com",
		"y",
		"1",
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
		"https://github.com/a/b.git",
		"Alice",
		"alice@example.com",
		"y",
		"1",
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
	globalDir := filepath.Join(home, ".config", "ws")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("defaults:\n  region: us-east1\n")
	if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), original, 0644); err != nil {
		t.Fatal(err)
	}
	// User config exists → only project prompts: project_id, github_repo_url, confirm
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
		if _, statErr := os.Stat(filepath.Join(home, ".config", "ws", "template", name)); statErr != nil {
			t.Errorf("%s not materialized in user template dir: %v", name, statErr)
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
		if _, statErr := os.Stat(filepath.Join(home, ".config", "ws", "template", name)); statErr != nil {
			t.Errorf("%s not materialized in user template dir: %v", name, statErr)
		}
	}
}
