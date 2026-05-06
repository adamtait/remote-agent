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
				ClusterName:        "ra-dev-cluster",
				ConfigName:         "ra-dev-config",
				ServiceAccountName: "ra-workstation-sa",
				ImageName:          "ra-workstation",
				ImageTag:           "latest",
				RepoName:           "ra-workstation-images",
				AutoSleepHours:     &sleep,
				AutoShutdownHours:  12,
			},
		},
		Secrets: map[string]config.SecretEntry{
			"example_secret": {SecretName: "example-secret", EnvVar: "EXAMPLE_TOKEN"},
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
	c.Secrets["example_secret"] = config.SecretEntry{SecretName: "x", EnvVar: "not-valid"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "env_var") {
		t.Errorf("want env_var-shape error, got %v", err)
	}
}

func TestValidate_SecretSecretNameNonEmpty(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["example_secret"] = config.SecretEntry{SecretName: "", EnvVar: "EXAMPLE_TOKEN"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "secret_name") {
		t.Errorf("want secret_name-missing error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_BetweenSecrets(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["another_secret"] = config.SecretEntry{SecretName: "another-secret", EnvVar: "ANOTHER_TOKEN"}
	c.Secrets["dup"] = config.SecretEntry{SecretName: "dup", EnvVar: "ANOTHER_TOKEN"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "ANOTHER_TOKEN") {
		t.Errorf("want collision error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_SecretAndEnv(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["another_secret"] = config.SecretEntry{SecretName: "another-secret", EnvVar: "ANOTHER_TOKEN"}
	c.Env = map[string]string{"ANOTHER_TOKEN": "whatever"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "ANOTHER_TOKEN") {
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

func TestValidateProject_EmptyIsOK(t *testing.T) {
	if err := config.ValidateProject(&config.ProjectConfig{}); err != nil {
		t.Errorf("empty project config should validate, got %v", err)
	}
}

func TestValidateProject_SkipsRequiredFieldChecks(t *testing.T) {
	// A typical project config has only overrides — no git, no project,
	// no full workstation block. ValidateProject must not flag those.
	p := &config.ProjectConfig{
		Gcloud: config.GcloudConfig{
			Workstation: config.WorkstationConfig{ConfigName: "override-name"},
		},
	}
	if err := config.ValidateProject(p); err != nil {
		t.Errorf("partial project config should validate, got %v", err)
	}
}

func TestValidateProject_CatchesEnvVarShape(t *testing.T) {
	p := &config.ProjectConfig{
		Secrets: map[string]config.SecretEntry{
			"foo": {SecretName: "foo", EnvVar: "lower-case"},
		},
	}
	err := config.ValidateProject(p)
	if err == nil || !strings.Contains(err.Error(), "env_var") {
		t.Errorf("want env_var-shape error, got %v", err)
	}
}

func TestValidateProject_CatchesIntraLayerCollision(t *testing.T) {
	p := &config.ProjectConfig{
		Secrets: map[string]config.SecretEntry{
			"a": {SecretName: "a", EnvVar: "DUPE"},
		},
		Env: map[string]string{"DUPE": "x"},
	}
	err := config.ValidateProject(p)
	if err == nil || !strings.Contains(err.Error(), "DUPE") {
		t.Errorf("want collision error, got %v", err)
	}
}

func TestValidateUser_FullValidCfg_OK(t *testing.T) {
	sleep := 2
	u := &config.UserConfig{
		Git: config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"},
		Gcloud: config.GcloudConfig{
			Project: "p",
			Workstation: config.WorkstationConfig{
				Region: "us-central1", MachineType: "e2-standard-4", DiskSizeGB: 50,
				ClusterName: "c", ConfigName: "cn", ServiceAccountName: "sa",
				ImageName: "img", ImageTag: "t", RepoName: "r",
				AutoSleepHours: &sleep, AutoShutdownHours: 12,
			},
		},
	}
	if err := config.ValidateUser(u, nil); err != nil {
		t.Errorf("expected OK, got %v", err)
	}
}

func TestValidateUser_CatchesMissingRequiredFields(t *testing.T) {
	u := &config.UserConfig{} // empty — missing everything
	err := config.ValidateUser(u, nil)
	if err == nil || !strings.Contains(err.Error(), "gcloud.project") {
		t.Errorf("want missing-required error, got %v", err)
	}
}

func TestValidateUser_ForwardsPluginErrs(t *testing.T) {
	sleep := 2
	u := &config.UserConfig{
		Git: config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"},
		Gcloud: config.GcloudConfig{
			Project: "p",
			Workstation: config.WorkstationConfig{
				Region: "us-central1", MachineType: "e2-standard-4", DiskSizeGB: 50,
				ClusterName: "c", ConfigName: "cn", ServiceAccountName: "sa",
				ImageName: "img", ImageTag: "t", RepoName: "r",
				AutoSleepHours: &sleep, AutoShutdownHours: 12,
			},
		},
	}
	err := config.ValidateUser(u, []string{"plugin-x: bad config"})
	if err == nil || !strings.Contains(err.Error(), "plugin-x") {
		t.Errorf("want plugin error forwarded, got %v", err)
	}
}
