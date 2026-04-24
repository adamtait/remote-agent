// ws/internal/config/validate.go
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// envVarPattern matches POSIX shell identifiers used as container env vars.
var envVarPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// reservedEnvVars are set directly by ws create; configs must not shadow them.
var reservedEnvVars = []string{
	"GCP_PROJECT_ID",
	"GITHUB_REPO_URL",
	"GIT_USER_NAME",
	"GIT_USER_EMAIL",
	"WS_SECRETS",
	"WS_CLAUDE_ENABLED",
	"WS_CODEX_ENABLED",
}

// Validate asserts the merged Config is complete and internally consistent.
// Run from cmd/root.go's PersistentPreRunE for every command except setup.
func Validate(cfg *Config) error {
	var errs []string

	required := []struct {
		label string
		empty bool
	}{
		{"gcloud.project", cfg.Gcloud.Project == ""},
		{"git.github_repo_url", cfg.Git.GithubRepoURL == ""},
		{"git.user_name", cfg.Git.UserName == ""},
		{"git.user_email", cfg.Git.UserEmail == ""},
		{"gcloud.workstation.region", cfg.Gcloud.Workstation.Region == ""},
		{"gcloud.workstation.machine_type", cfg.Gcloud.Workstation.MachineType == ""},
		{"gcloud.workstation.disk_size_gb", cfg.Gcloud.Workstation.DiskSizeGB == 0},
		{"gcloud.workstation.cluster_name", cfg.Gcloud.Workstation.ClusterName == ""},
		{"gcloud.workstation.config_name", cfg.Gcloud.Workstation.ConfigName == ""},
		{"gcloud.workstation.service_account_name", cfg.Gcloud.Workstation.ServiceAccountName == ""},
		{"gcloud.workstation.image_name", cfg.Gcloud.Workstation.ImageName == ""},
		{"gcloud.workstation.image_tag", cfg.Gcloud.Workstation.ImageTag == ""},
		{"gcloud.workstation.repo_name", cfg.Gcloud.Workstation.RepoName == ""},
		{"gcloud.workstation.auto_sleep_hours", cfg.Gcloud.Workstation.AutoSleepHours == nil},
		{"gcloud.workstation.auto_shutdown_hours", cfg.Gcloud.Workstation.AutoShutdownHours == 0},
	}
	for _, r := range required {
		if r.empty {
			errs = append(errs, fmt.Sprintf("%s is required (run 'ws setup' to write it)", r.label))
		}
	}

	// Claude harness: enabled flag gates auth_mode + claude_* secret requirements.
	errs = append(errs, validateClaude(cfg)...)

	// github_pat required (shared infrastructure, not Claude-specific).
	if _, ok := cfg.Secrets["github_pat"]; !ok {
		errs = append(errs, "secrets.github_pat is required")
	}

	// Per-secret shape + collision detection.
	usedEnvVars := map[string]string{}
	for _, r := range reservedEnvVars {
		usedEnvVars[r] = "(ws-reserved)"
	}

	for name, s := range cfg.Secrets {
		if s.SecretName == "" {
			errs = append(errs, fmt.Sprintf("secrets.%s.secret_name is required", name))
		}
		if s.EnvVar == "" {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var is required", name))
		} else if !envVarPattern.MatchString(s.EnvVar) {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q must match %s", name, s.EnvVar, envVarPattern))
		} else if strings.HasPrefix(s.EnvVar, "WS_SECRET_") {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q collides with ws-reserved WS_SECRET_* prefix", name, s.EnvVar))
		} else if existing, collide := usedEnvVars[s.EnvVar]; collide {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q collides with %s", name, s.EnvVar, existing))
		} else {
			usedEnvVars[s.EnvVar] = "secrets." + name
		}
	}

	for key := range cfg.Env {
		if !envVarPattern.MatchString(key) {
			errs = append(errs, fmt.Sprintf("env.%s name must match %s", key, envVarPattern))
			continue
		}
		if strings.HasPrefix(key, "WS_SECRET_") {
			errs = append(errs, fmt.Sprintf("env.%s collides with ws-reserved WS_SECRET_* prefix", key))
			continue
		}
		if existing, collide := usedEnvVars[key]; collide {
			errs = append(errs, fmt.Sprintf("env.%s collides with %s", key, existing))
			continue
		}
		usedEnvVars[key] = "env." + key
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("config validation failed:\n  - " + strings.Join(errs, "\n  - "))
}

// validateClaude enforces the claude.enabled ↔ secrets.claude_* contract.
// Legacy configs (no claude: block) that still have claude_* secrets get a
// targeted migration error pointing at `ws setup --force`.
func validateClaude(cfg *Config) []string {
	var errs []string
	_, hasOAuth := cfg.Secrets["claude_oauth"]
	_, hasAPIKey := cfg.Secrets["claude_apikey"]

	// Migration signal: claude section absent but claude_* secrets present.
	if cfg.Claude.Enabled == nil && (hasOAuth || hasAPIKey) {
		errs = append(errs, "claude.enabled is not set but claude_* secrets are configured; "+
			"add 'claude:\\n  enabled: true\\n  auth_mode: oauth|apikey' to ~/.config/ws/config.yaml "+
			"or run 'ws setup --force' to regenerate")
		return errs
	}

	if cfg.Claude.IsEnabled() {
		switch cfg.Claude.AuthMode {
		case "oauth":
			if !hasOAuth {
				errs = append(errs, "claude.enabled=true with auth_mode=oauth requires secrets.claude_oauth")
			}
			if hasAPIKey {
				errs = append(errs, "claude.auth_mode=oauth but secrets.claude_apikey is also set; remove one")
			}
		case "apikey":
			if !hasAPIKey {
				errs = append(errs, "claude.enabled=true with auth_mode=apikey requires secrets.claude_apikey")
			}
			if hasOAuth {
				errs = append(errs, "claude.auth_mode=apikey but secrets.claude_oauth is also set; remove one")
			}
		case "":
			errs = append(errs, "claude.auth_mode is required when claude.enabled=true (oauth or apikey)")
		default:
			errs = append(errs, fmt.Sprintf("claude.auth_mode must be \"oauth\" or \"apikey\", got %q", cfg.Claude.AuthMode))
		}
		return errs
	}

	// Disabled (explicitly or absent-plus-no-claude-secrets): claude_* must not be present.
	if hasOAuth {
		errs = append(errs, "secrets.claude_oauth is set but claude.enabled is not true; set claude.enabled=true (auth_mode=oauth) or remove the secret")
	}
	if hasAPIKey {
		errs = append(errs, "secrets.claude_apikey is set but claude.enabled is not true; set claude.enabled=true (auth_mode=apikey) or remove the secret")
	}
	return errs
}
