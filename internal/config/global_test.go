package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestLoadUserConfig_Missing_ReturnsEmpty(t *testing.T) {
	cfg, err := config.LoadUserConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("LoadUserConfig on missing file: got %v, want nil", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil empty UserConfig")
	}
	if cfg.Git.UserName != "" || len(cfg.Secrets) != 0 {
		t.Errorf("want zero-value UserConfig, got %+v", cfg)
	}
}

func TestLoadUserConfig_Malformed_ReturnsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("git:\n  user_name: [unterminated"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadUserConfig(p); err == nil {
		t.Fatal("expected YAML parse error, got nil")
	}
}

func TestLoadUserConfig_Roundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	contents := `
git:
  user_name: "Alice"
  user_email: "alice@example.com"
  github_repo_url: https://example.com/user.git
gcloud:
  project: user-proj
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
secrets:
  example_secret:
    secret_name: example-secret
    env_var: EXAMPLE_TOKEN
  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN
env:
  EXTRA_KEY: extra_value
`
	if err := os.WriteFile(p, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadUserConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.UserName != "Alice" ||
		cfg.Gcloud.Project != "user-proj" || cfg.Gcloud.Workstation.Region != "us-central1" ||
		cfg.Gcloud.Workstation.AutoSleepHours == nil || *cfg.Gcloud.Workstation.AutoSleepHours != 2 {
		t.Errorf("parsed wrong: %+v", cfg)
	}
	if got := cfg.Secrets["example_secret"].EnvVar; got != "EXAMPLE_TOKEN" {
		t.Errorf("example_secret.env_var: got %q", got)
	}
	if cfg.Env["EXTRA_KEY"] != "extra_value" {
		t.Errorf("env map not parsed: %+v", cfg.Env)
	}
}

func TestLoadProjectConfig_Roundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	contents := `
git:
  github_repo_url: https://example.com/x.git
gcloud:
  project: my-proj
  workstation:
    machine_type: e2-standard-8
secrets:
  my_extra:
    secret_name: my-extra
    env_var: MY_EXTRA
env:
  MY_VAR: 1
`
	if err := os.WriteFile(p, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadProjectConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gcloud.Project != "my-proj" {
		t.Errorf("config parsed wrong: %+v", cfg)
	}
	if cfg.Gcloud.Workstation.MachineType != "e2-standard-8" {
		t.Errorf("project workstation override lost: %+v", cfg.Gcloud.Workstation)
	}
	if cfg.Secrets["my_extra"].EnvVar != "MY_EXTRA" || cfg.Env["MY_VAR"] != "1" {
		t.Errorf("maps not parsed: %+v", cfg)
	}
}

func TestLoadProjectConfig_Missing_ReturnsError(t *testing.T) {
	if _, err := config.LoadProjectConfig(filepath.Join(t.TempDir(), "none.yaml")); err == nil {
		t.Fatal("expected error for missing project config, got nil")
	}
}

