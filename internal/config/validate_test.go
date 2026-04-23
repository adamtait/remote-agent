package config_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func fullValidCfg() *config.Config {
	sleep := 2
	return &config.Config{
		ProjectID:          "p",
		GithubRepoURL:      "https://example.com/r.git",
		GitUserName:        "Alice",
		GitUserEmail:       "a@x.com",
		Region:             "us-central1",
		MachineType:        "e2-standard-4",
		DiskSizeGB:         50,
		ClusterName:        "claude-dev-cluster",
		ConfigName:         "claude-dev-config",
		ServiceAccountName: "claude-workstation-sa",
		ImageName:          "claude-code-workstation",
		ImageTag:           "latest",
		RepoName:           "claude-workstation-images",
		AutoSleepHours:     &sleep,
		AutoShutdownHours:  12,
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
			"github_pat":   {SecretName: "github-pat", EnvVar: "GH_TOKEN"},
		},
	}
}

func TestValidate_FullConfig_OK(t *testing.T) {
	if err := config.Validate(fullValidCfg()); err != nil {
		t.Errorf("expected OK, got %v", err)
	}
}

func TestValidate_MissingRequiredKeys(t *testing.T) {
	tcs := []struct {
		mutate func(*config.Config)
		want   string
	}{
		{func(c *config.Config) { c.ProjectID = "" }, "project.id"},
		{func(c *config.Config) { c.GithubRepoURL = "" }, "project.github_repo_url"},
		{func(c *config.Config) { c.GitUserName = "" }, "git.user_name"},
		{func(c *config.Config) { c.GitUserEmail = "" }, "git.user_email"},
		{func(c *config.Config) { c.Region = "" }, "workstation.region"},
		{func(c *config.Config) { c.MachineType = "" }, "workstation.machine_type"},
		{func(c *config.Config) { c.DiskSizeGB = 0 }, "workstation.disk_size_gb"},
		{func(c *config.Config) { c.ClusterName = "" }, "workstation.cluster_name"},
		{func(c *config.Config) { c.ConfigName = "" }, "workstation.config_name"},
		{func(c *config.Config) { c.ServiceAccountName = "" }, "workstation.service_account_name"},
		{func(c *config.Config) { c.ImageName = "" }, "workstation.image_name"},
		{func(c *config.Config) { c.ImageTag = "" }, "workstation.image_tag"},
		{func(c *config.Config) { c.RepoName = "" }, "workstation.repo_name"},
		{func(c *config.Config) { c.AutoSleepHours = nil }, "workstation.auto_sleep_hours"},
		{func(c *config.Config) { c.AutoShutdownHours = 0 }, "workstation.auto_shutdown_hours"},
	}
	for _, tc := range tcs {
		c := fullValidCfg()
		tc.mutate(c)
		err := config.Validate(c)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want error mentioning %q, got %v", tc.want, err)
		}
	}
}

func TestValidate_ExactlyOneClaudeAuth(t *testing.T) {
	c := fullValidCfg()
	delete(c.Secrets, "claude_oauth")
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "claude_oauth") {
		t.Errorf("want missing-claude-auth error, got %v", err)
	}

	c = fullValidCfg()
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("want exactly-one error with both set, got %v", err)
	}
}

func TestValidate_GithubPatRequired(t *testing.T) {
	c := fullValidCfg()
	delete(c.Secrets, "github_pat")
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "github_pat") {
		t.Errorf("want missing-github_pat error, got %v", err)
	}
}

func TestValidate_SecretEnvVarShape(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_oauth"] = config.SecretEntry{SecretName: "x", EnvVar: "not-valid"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "env_var") {
		t.Errorf("want env_var-shape error, got %v", err)
	}
}

func TestValidate_SecretSecretNameNonEmpty(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_oauth"] = config.SecretEntry{SecretName: "", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "secret_name") {
		t.Errorf("want secret_name-missing error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_BetweenSecrets(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["extra"] = config.SecretEntry{SecretName: "ex", EnvVar: "GH_TOKEN"} // collides with github_pat
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Errorf("want collision error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_SecretAndEnv(t *testing.T) {
	c := fullValidCfg()
	c.Env = map[string]string{"GH_TOKEN": "whatever"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Errorf("want collision error between secret and env, got %v", err)
	}
}

func TestValidate_EnvVarCollision_Reserved(t *testing.T) {
	for _, reserved := range []string{"GCP_PROJECT_ID", "GITHUB_REPO_URL", "GIT_USER_NAME", "WS_SECRETS", "WS_SECRET_FOO_NAME"} {
		c := fullValidCfg()
		c.Env = map[string]string{reserved: "v"}
		if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), reserved) {
			t.Errorf("reserved %q not caught: %v", reserved, err)
		}
	}
}

func TestValidate_EnvKeyShape(t *testing.T) {
	c := fullValidCfg()
	c.Env = map[string]string{"not-valid": "x"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "env") {
		t.Errorf("want env key-shape error, got %v", err)
	}
}
