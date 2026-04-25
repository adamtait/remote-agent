package config_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func fullValidCfg() *config.Config {
	sleep := 2
	return &config.Config{
		Git: config.GitConfig{
			UserName:      "Alice",
			UserEmail:     "a@x.com",
			GithubRepoURL: "https://example.com/r.git",
		},
		Gcloud: config.GcloudConfig{
			Project: "p",
			Workstation: config.WorkstationConfig{
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
			},
		},
		Claude: config.ClaudeConfig{
			Enabled:  boolPtr(true),
			AuthMode: "oauth",
		},
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
		{func(c *config.Config) { c.Gcloud.Project = "" }, "gcloud.project"},
		{func(c *config.Config) { c.Git.GithubRepoURL = "" }, "git.github_repo_url"},
		{func(c *config.Config) { c.Git.UserName = "" }, "git.user_name"},
		{func(c *config.Config) { c.Git.UserEmail = "" }, "git.user_email"},
		{func(c *config.Config) { c.Gcloud.Workstation.Region = "" }, "gcloud.workstation.region"},
		{func(c *config.Config) { c.Gcloud.Workstation.MachineType = "" }, "gcloud.workstation.machine_type"},
		{func(c *config.Config) { c.Gcloud.Workstation.DiskSizeGB = 0 }, "gcloud.workstation.disk_size_gb"},
		{func(c *config.Config) { c.Gcloud.Workstation.ClusterName = "" }, "gcloud.workstation.cluster_name"},
		{func(c *config.Config) { c.Gcloud.Workstation.ConfigName = "" }, "gcloud.workstation.config_name"},
		{func(c *config.Config) { c.Gcloud.Workstation.ServiceAccountName = "" }, "gcloud.workstation.service_account_name"},
		{func(c *config.Config) { c.Gcloud.Workstation.ImageName = "" }, "gcloud.workstation.image_name"},
		{func(c *config.Config) { c.Gcloud.Workstation.ImageTag = "" }, "gcloud.workstation.image_tag"},
		{func(c *config.Config) { c.Gcloud.Workstation.RepoName = "" }, "gcloud.workstation.repo_name"},
		{func(c *config.Config) { c.Gcloud.Workstation.AutoSleepHours = nil }, "gcloud.workstation.auto_sleep_hours"},
		{func(c *config.Config) { c.Gcloud.Workstation.AutoShutdownHours = 0 }, "gcloud.workstation.auto_shutdown_hours"},
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

func TestValidate_Claude_EnabledOAuth_OK(t *testing.T) {
	c := fullValidCfg()
	// fullValidCfg already sets enabled=true + auth_mode=oauth + claude_oauth.
	if err := config.Validate(c); err != nil {
		t.Errorf("expected OK, got %v", err)
	}
}

func TestValidate_Claude_EnabledAPIKey_OK(t *testing.T) {
	c := fullValidCfg()
	c.Claude.AuthMode = "apikey"
	delete(c.Secrets, "claude_oauth")
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	if err := config.Validate(c); err != nil {
		t.Errorf("expected OK, got %v", err)
	}
}

func TestValidate_Claude_Disabled_OK(t *testing.T) {
	c := fullValidCfg()
	c.Claude = config.ClaudeConfig{Enabled: boolPtr(false)}
	delete(c.Secrets, "claude_oauth")
	if err := config.Validate(c); err != nil {
		t.Errorf("expected OK when claude disabled and no claude_* secrets, got %v", err)
	}
}

func TestValidate_Claude_EnabledMissingMatchingSecret(t *testing.T) {
	c := fullValidCfg()
	delete(c.Secrets, "claude_oauth")
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "claude_oauth") {
		t.Errorf("want missing claude_oauth error, got %v", err)
	}
}

func TestValidate_Claude_EnabledAuthModeMismatch(t *testing.T) {
	c := fullValidCfg()
	c.Claude.AuthMode = "apikey"
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "claude_apikey") {
		t.Errorf("want missing claude_apikey error, got %v", err)
	}
}

func TestValidate_Claude_EnabledBothSecretsPresent(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "claude_apikey") {
		t.Errorf("want both-secrets error, got %v", err)
	}
}

func TestValidate_Claude_EnabledMissingAuthMode(t *testing.T) {
	c := fullValidCfg()
	c.Claude.AuthMode = ""
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "auth_mode") {
		t.Errorf("want auth_mode-required error, got %v", err)
	}
}

func TestValidate_Claude_EnabledBadAuthMode(t *testing.T) {
	c := fullValidCfg()
	c.Claude.AuthMode = "nope"
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "auth_mode") {
		t.Errorf("want bad-auth_mode error, got %v", err)
	}
}

func TestValidate_Claude_DisabledButClaudeSecretPresent(t *testing.T) {
	c := fullValidCfg()
	c.Claude = config.ClaudeConfig{Enabled: boolPtr(false)}
	// claude_oauth still in secrets from fullValidCfg
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "claude_oauth") {
		t.Errorf("want claude_oauth-present-but-disabled error, got %v", err)
	}
}

func TestValidate_Claude_MigrationSignal(t *testing.T) {
	// Legacy config: no claude: block but claude_oauth secret present.
	c := fullValidCfg()
	c.Claude = config.ClaudeConfig{} // Enabled = nil
	err := config.Validate(c)
	if err == nil || !strings.Contains(err.Error(), "claude.enabled is not set") {
		t.Errorf("want migration-signal error, got %v", err)
	}
	if !strings.Contains(err.Error(), "ra setup --force") {
		t.Errorf("migration error should point at 'ra setup --force', got %v", err)
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
	for _, reserved := range []string{"GCP_PROJECT_ID", "GITHUB_REPO_URL", "GIT_USER_NAME", "RA_SECRETS", "RA_SECRET_FOO_NAME", "RA_CLAUDE_ENABLED", "RA_CODEX_ENABLED"} {
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
