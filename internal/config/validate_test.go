package config_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func fullValidCfg() *config.Config {
	sleep := 2
	return &config.Config{
		Git: config.GitConfig{
			UserName:  "Alice",
			UserEmail: "a@x.com",
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
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
		},
	}
}

func TestValidate_FullConfig_OK(t *testing.T) {
	c := fullValidCfg()
	if err := config.Validate(c, nil); err != nil {
		t.Errorf("expected OK, got %v", err)
	}
}

func TestValidate_PluginErrorsAreForwarded(t *testing.T) {
	c := fullValidCfg()
	err := config.Validate(c, []string{"some-plugin-reported-error"})
	if err == nil || !strings.Contains(err.Error(), "some-plugin-reported-error") {
		t.Errorf("expected plugin error to be forwarded, got %v", err)
	}
}

func TestValidate_MissingRequiredKeys(t *testing.T) {
	tcs := []struct {
		mutate func(*config.Config)
		want   string
	}{
		{func(c *config.Config) { c.Gcloud.Project = "" }, "gcloud.project"},
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
		err := config.Validate(c, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want error mentioning %q, got %v", tc.want, err)
		}
	}
}

func TestValidate_SecretEnvVarShape(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_oauth"] = config.SecretEntry{SecretName: "x", EnvVar: "not-valid"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "env_var") {
		t.Errorf("want env_var-shape error, got %v", err)
	}
}

func TestValidate_SecretSecretNameNonEmpty(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_oauth"] = config.SecretEntry{SecretName: "", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "secret_name") {
		t.Errorf("want secret_name-missing error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_BetweenSecrets(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	c.Secrets["dup"] = config.SecretEntry{SecretName: "dup", EnvVar: "ANTHROPIC_API_KEY"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("want collision error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_SecretAndEnv(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	c.Env = map[string]string{"ANTHROPIC_API_KEY": "whatever"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("want collision error between secret and env, got %v", err)
	}
}

func TestValidate_EnvVarCollision_Reserved(t *testing.T) {
	for _, reserved := range []string{"GCP_PROJECT_ID", "GIT_USER_NAME", "RA_SECRETS", "RA_SECRET_FOO_NAME"} {
		c := fullValidCfg()
		c.Env = map[string]string{reserved: "v"}
		if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), reserved) {
			t.Errorf("reserved %q not caught: %v", reserved, err)
		}
	}
}

func TestValidate_EnvKeyShape(t *testing.T) {
	c := fullValidCfg()
	c.Env = map[string]string{"not-valid": "x"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "env") {
		t.Errorf("want env key-shape error, got %v", err)
	}
}
