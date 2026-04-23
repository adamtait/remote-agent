package config_test

import (
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestMerge_UserBaselineNoProject(t *testing.T) {
	two := 2
	user := &config.UserConfig{
		Git: config.GitConfig{UserName: "Alice", UserEmail: "alice@example.com"},
		Workstation: config.WorkstationConfig{
			Region: "us-central1", MachineType: "e2-standard-4", DiskSizeGB: 50,
			ClusterName: "claude-dev-cluster", ConfigName: "claude-dev-config",
			ServiceAccountName: "claude-workstation-sa",
			ImageName:          "claude-code-workstation",
			ImageTag:           "latest",
			RepoName:           "claude-workstation-images",
			AutoSleepHours:     &two,
			AutoShutdownHours:  12,
		},
	}
	cfg, err := config.Merge(user, &config.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-central1" || cfg.MachineType != "e2-standard-4" ||
		cfg.GitUserName != "Alice" || cfg.DiskSizeGB != 50 ||
		cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 2 ||
		cfg.AutoShutdownHours != 12 {
		t.Errorf("unexpected merged Config: %+v", cfg)
	}
}

func TestMerge_ProjectOverridesUser(t *testing.T) {
	one := 1
	eight := 8
	user := &config.UserConfig{
		Workstation: config.WorkstationConfig{
			Region:            "us-central1",
			MachineType:       "e2-standard-4",
			AutoSleepHours:    &eight,
			AutoShutdownHours: 12,
		},
	}
	project := &config.ProjectConfig{
		Project: config.ProjectSection{ID: "my-proj", GithubRepoURL: "https://example.com/r.git"},
		Workstation: config.WorkstationConfig{
			Region:            "us-east1",
			AutoSleepHours:    &one,
			AutoShutdownHours: 24,
		},
	}
	cfg, err := config.Merge(user, project)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east1" {
		t.Errorf("Region: got %q, want us-east1", cfg.Region)
	}
	if cfg.MachineType != "e2-standard-4" {
		t.Errorf("MachineType: got %q, want e2-standard-4 (from user)", cfg.MachineType)
	}
	if cfg.ProjectID != "my-proj" || cfg.GithubRepoURL != "https://example.com/r.git" {
		t.Errorf("project fields missing: %+v", cfg)
	}
	if cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 1 {
		t.Errorf("AutoSleepHours: got %v, want &1", cfg.AutoSleepHours)
	}
	if cfg.AutoShutdownHours != 24 {
		t.Errorf("AutoShutdownHours: got %d, want 24", cfg.AutoShutdownHours)
	}
}

func TestMerge_EmptyProjectDoesNotClobberUser(t *testing.T) {
	user := &config.UserConfig{
		Workstation: config.WorkstationConfig{Region: "us-central1", MachineType: "e2-standard-4"},
	}
	cfg, _ := config.Merge(user, &config.ProjectConfig{})
	if cfg.Region != "us-central1" || cfg.MachineType != "e2-standard-4" {
		t.Errorf("zero-valued project fields should not override user: %+v", cfg)
	}
}

func TestMerge_NoHardcodedDefaultsWhenBothEmpty(t *testing.T) {
	cfg, _ := config.Merge(&config.UserConfig{}, &config.ProjectConfig{})
	// Every workstation field should be zero — no silent fallback to
	// "e2-standard-4" or similar. Validation (not Merge) flags missing.
	if cfg.MachineType != "" || cfg.ClusterName != "" || cfg.ImageName != "" ||
		cfg.ServiceAccountName != "" || cfg.DiskSizeGB != 0 {
		t.Errorf("Merge must not inject hardcoded defaults, got %+v", cfg)
	}
}

func TestMerge_SecretsMap_UserSeedsProjectOverrides(t *testing.T) {
	user := &config.UserConfig{
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
			"github_pat":   {SecretName: "github-pat", EnvVar: "GH_TOKEN"},
		},
	}
	project := &config.ProjectConfig{
		Secrets: map[string]config.SecretEntry{
			"github_pat": {SecretName: "proj-github-pat", EnvVar: "GH_TOKEN"}, // override
			"my_api":     {SecretName: "my-api", EnvVar: "MY_API"},             // add
		},
	}
	cfg, _ := config.Merge(user, project)
	if got := cfg.Secrets["claude_oauth"].SecretName; got != "claude-auth" {
		t.Errorf("claude_oauth.secret_name: got %q, want claude-auth (from user)", got)
	}
	if got := cfg.Secrets["github_pat"].SecretName; got != "proj-github-pat" {
		t.Errorf("github_pat.secret_name: got %q, want proj-github-pat (project override)", got)
	}
	if got := cfg.Secrets["my_api"].SecretName; got != "my-api" {
		t.Errorf("my_api.secret_name: got %q, want my-api (project add)", got)
	}
	if len(cfg.Secrets) != 3 {
		t.Errorf("want 3 secrets, got %d: %+v", len(cfg.Secrets), cfg.Secrets)
	}
}

func TestMerge_EnvMap_UserSeedsProjectOverrides(t *testing.T) {
	user := &config.UserConfig{Env: map[string]string{"FOO": "1", "BAR": "u"}}
	project := &config.ProjectConfig{Env: map[string]string{"BAR": "p", "BAZ": "3"}}
	cfg, _ := config.Merge(user, project)
	want := map[string]string{"FOO": "1", "BAR": "p", "BAZ": "3"}
	for k, v := range want {
		if cfg.Env[k] != v {
			t.Errorf("Env[%q]: got %q, want %q", k, cfg.Env[k], v)
		}
	}
	if len(cfg.Env) != 3 {
		t.Errorf("want 3 env entries, got %d", len(cfg.Env))
	}
}

func TestMerge_GitIdentityUserOnly(t *testing.T) {
	user := &config.UserConfig{Git: config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"}}
	cfg, _ := config.Merge(user, &config.ProjectConfig{})
	if cfg.GitUserName != "Alice" || cfg.GitUserEmail != "a@x.com" {
		t.Errorf("git identity lost: %+v", cfg)
	}
}
