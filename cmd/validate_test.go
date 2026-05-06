package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cmd"
)

// validUserConfigYAML returns a minimally complete user config that passes
// the full Validate check.
func validUserConfigYAML() string {
	return `git:
  user_name: Alice
  user_email: a@example.com
gcloud:
  project: my-project
  workstation:
    region: us-central1
    machine_type: e2-standard-4
    disk_size_gb: 50
    cluster_name: ra-dev-cluster
    config_name: ra-dev-config
    service_account_name: ra-workstation-sa
    image_name: ra-workstation
    image_tag: latest
    repo_name: ra-workstation-images
    auto_sleep_hours: 2
    auto_shutdown_hours: 12
`
}

// setupValidateEnv writes a user config and chdir's into a fresh tmp dir.
// Returns (cwd, home). Optionally writes a .ra/config.yaml under cwd if
// projectYAML is non-empty.
func setupValidateEnv(t *testing.T, projectYAML string) (string, string) {
	t.Helper()
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "ra")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "config.yaml"), []byte(validUserConfigYAML()), 0o644); err != nil {
		t.Fatal(err)
	}

	if projectYAML != "" {
		projectDir := filepath.Join(cwd, ".ra")
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, "config.yaml"), []byte(projectYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	return cwd, home
}

func TestValidate_AllGood_NoProjectConfig(t *testing.T) {
	setupValidateEnv(t, "")

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"validate"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "user config:") || !strings.Contains(out, "OK") {
		t.Errorf("expected user OK line, got: %q", out)
	}
	if !strings.Contains(out, "(none — no .ra/config.yaml found)") {
		t.Errorf("expected 'no project config' notice, got: %q", out)
	}
	if !strings.Contains(out, "merged config:  OK") {
		t.Errorf("expected merged OK line, got: %q", out)
	}
}

func TestValidate_AllGood_WithProjectConfig(t *testing.T) {
	projectYAML := `gcloud:
  workstation:
    config_name: project-override
`
	setupValidateEnv(t, projectYAML)

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"validate"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"user config:", "project config:", "merged config:  OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %q", want, out)
		}
	}
	if strings.Count(out, "OK") < 3 {
		t.Errorf("expected three OK lines, got: %q", out)
	}
}

func TestValidate_ProjectStructuralFailure(t *testing.T) {
	// Lowercase env_var fails structural check; user is fine.
	projectYAML := `secrets:
  bad:
    secret_name: bad
    env_var: lower-case
`
	setupValidateEnv(t, projectYAML)

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"validate"})
	if code == 0 {
		t.Fatalf("expected non-zero exit, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "project config:") || !strings.Contains(out, "FAIL") {
		t.Errorf("expected project FAIL line, got: %q", out)
	}
	if !strings.Contains(out, "lower-case") {
		t.Errorf("expected the offending value in error, got: %q", out)
	}
	// Merged should also fail with the same error since project layer is merged in.
	if !strings.Contains(out, "merged config:  FAIL") {
		t.Errorf("expected merged FAIL line, got: %q", out)
	}
}

func TestValidate_UserMissingRequired_RunsAnyway(t *testing.T) {
	// Write a deliberately broken user config (missing gcloud.project)
	// and assert: (1) command still runs, (2) user line shows FAIL,
	// (3) exit is non-zero. This proves the preflight bypass works.
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "ra")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := `git:
  user_name: Alice
  user_email: a@example.com
`
	if err := os.WriteFile(filepath.Join(userDir, "config.yaml"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"validate"})
	if code == 0 {
		t.Fatalf("expected non-zero exit, stdout=%q", stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "user config:") || !strings.Contains(out, "FAIL") {
		t.Errorf("expected user FAIL line, got: %q", out)
	}
	if !strings.Contains(out, "gcloud.project") {
		t.Errorf("expected missing-required error mentioning gcloud.project, got: %q", out)
	}
}

func TestValidate_ShowFlag_PrintsMergedYAML(t *testing.T) {
	setupValidateEnv(t, "")

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"validate", "--show"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "--- merged config ---") {
		t.Errorf("expected merged config marker, got: %q", out)
	}
	// Spot-check that the merged YAML actually includes a known field.
	if !strings.Contains(out, "my-project") {
		t.Errorf("expected merged YAML to contain project name, got: %q", out)
	}
}
