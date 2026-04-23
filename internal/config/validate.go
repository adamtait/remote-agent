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
}

// Validate asserts the merged Config is complete and internally consistent.
// Run from cmd/root.go's PersistentPreRunE for every command except setup.
func Validate(cfg *Config) error {
	var errs []string

	required := []struct {
		label string
		empty bool
	}{
		{"project.id", cfg.ProjectID == ""},
		{"project.github_repo_url", cfg.GithubRepoURL == ""},
		{"git.user_name", cfg.GitUserName == ""},
		{"git.user_email", cfg.GitUserEmail == ""},
		{"workstation.region", cfg.Region == ""},
		{"workstation.machine_type", cfg.MachineType == ""},
		{"workstation.disk_size_gb", cfg.DiskSizeGB == 0},
		{"workstation.cluster_name", cfg.ClusterName == ""},
		{"workstation.config_name", cfg.ConfigName == ""},
		{"workstation.service_account_name", cfg.ServiceAccountName == ""},
		{"workstation.image_name", cfg.ImageName == ""},
		{"workstation.image_tag", cfg.ImageTag == ""},
		{"workstation.repo_name", cfg.RepoName == ""},
		{"workstation.auto_sleep_hours", cfg.AutoSleepHours == nil},
		{"workstation.auto_shutdown_hours", cfg.AutoShutdownHours == 0},
	}
	for _, r := range required {
		if r.empty {
			errs = append(errs, fmt.Sprintf("%s is required (run 'ws setup' to write it)", r.label))
		}
	}

	// Claude auth — exactly one of oauth/apikey.
	_, hasOAuth := cfg.Secrets["claude_oauth"]
	_, hasAPIKey := cfg.Secrets["claude_apikey"]
	switch {
	case !hasOAuth && !hasAPIKey:
		errs = append(errs, "secrets.claude_oauth or secrets.claude_apikey is required")
	case hasOAuth && hasAPIKey:
		errs = append(errs, "exactly one of secrets.claude_oauth or secrets.claude_apikey (got both)")
	}

	// github_pat required.
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
