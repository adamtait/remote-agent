// ws/internal/config/global_test.go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestLoadGlobalConfig_ParsesAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
defaults:
  region: us-west1
  git_user_name: Test User
  git_user_email: test@example.com
projects:
  my-project:
    service_account_name: my-sa
    image_name: my-image
    image_tag: v1
    repo_name: my-repo
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Defaults.Region != "us-west1" {
		t.Errorf("region: got %q, want %q", cfg.Defaults.Region, "us-west1")
	}
	if cfg.Defaults.GitUserName != "Test User" {
		t.Errorf("git_user_name: got %q, want %q", cfg.Defaults.GitUserName, "Test User")
	}
	override := cfg.Projects["my-project"]
	if override.ServiceAccountName != "my-sa" {
		t.Errorf("service_account_name: got %q, want %q", override.ServiceAccountName, "my-sa")
	}
	if override.ImageTag != "v1" {
		t.Errorf("image_tag: got %q, want %q", override.ImageTag, "v1")
	}
}

func TestLoadGlobalConfig_MissingFileReturnsEmpty(t *testing.T) {
	cfg, err := config.LoadGlobalConfig("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if cfg.Defaults.Region != "" {
		t.Errorf("expected empty config, got region %q", cfg.Defaults.Region)
	}
}
