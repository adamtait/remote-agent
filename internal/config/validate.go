// ra/internal/config/validate.go
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// envVarPattern matches POSIX shell identifiers used as container env vars.
var envVarPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// reservedEnvVars are set directly by ra create; configs must not shadow them.
var reservedEnvVars = []string{
	"GCP_PROJECT_ID",
	"GIT_USER_NAME",
	"GIT_USER_EMAIL",
	"RA_SECRETS",
}

// Validate asserts the merged Config is complete and internally consistent.
// pluginErrs are additional errors from registered plugins.
// Run from cmd/root.go's PersistentPreRunE for every command except setup.
func Validate(cfg *Config, pluginErrs []string) error {
	var errs []string

	required := []struct {
		label string
		empty bool
	}{
		{"gcloud.project", cfg.Gcloud.Project == ""},
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
			errs = append(errs, fmt.Sprintf("%s is required (run 'ra setup' to write it)", r.label))
		}
	}

	if strings.ContainsAny(cfg.Connect.LoginCommand, "\n\r") {
		errs = append(errs, "connect.login_command must be a single line")
	}

	errs = append(errs, pluginErrs...)
	errs = append(errs, validateSecretsAndEnv(cfg.Secrets, cfg.Env)...)

	if len(errs) == 0 {
		return nil
	}
	return errors.New("config validation failed:\n  - " + strings.Join(errs, "\n  - "))
}

// ValidateProject runs structural checks only — secrets/env shape and
// collisions. Required-field checks are deliberately skipped because project
// configs are typically partial overrides.
func ValidateProject(p *ProjectConfig) error {
	if p == nil {
		return nil
	}
	errs := validateSecretsAndEnv(p.Secrets, p.Env)
	if len(errs) == 0 {
		return nil
	}
	return errors.New("project config validation failed:\n  - " + strings.Join(errs, "\n  - "))
}

// ValidateUser runs the full check (required fields + structural + plugins)
// against the user-level baseline config in isolation.
func ValidateUser(u *UserConfig, pluginErrs []string) error {
	if u == nil {
		return errors.New("user config is nil")
	}
	cfg := &Config{
		Git:     u.Git,
		Gcloud:  u.Gcloud,
		Connect: u.Connect,
		Plugins: u.Plugins,
		Secrets: u.Secrets,
		Env:     u.Env,
	}
	return Validate(cfg, pluginErrs)
}

// validateSecretsAndEnv runs the structural checks shared by all layers:
// secret shape, env-var shape, reserved-prefix bans, and intra-layer
// collisions between secrets, env entries, and ra-reserved env vars.
func validateSecretsAndEnv(secrets map[string]SecretEntry, env map[string]string) []string {
	var errs []string

	usedEnvVars := map[string]string{}
	for _, r := range reservedEnvVars {
		usedEnvVars[r] = "(ra-reserved)"
	}

	for name, s := range secrets {
		if s.SecretName == "" {
			errs = append(errs, fmt.Sprintf("secrets.%s.secret_name is required", name))
		}
		if s.EnvVar == "" {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var is required", name))
		} else if !envVarPattern.MatchString(s.EnvVar) {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q must match %s", name, s.EnvVar, envVarPattern))
		} else if strings.HasPrefix(s.EnvVar, "RA_SECRET_") {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q collides with ra-reserved RA_SECRET_* prefix", name, s.EnvVar))
		} else if strings.HasPrefix(s.EnvVar, "RA_PLUGIN_") {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q collides with ra-reserved RA_PLUGIN_* prefix", name, s.EnvVar))
		} else if existing, collide := usedEnvVars[s.EnvVar]; collide {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q collides with %s", name, s.EnvVar, existing))
		} else {
			usedEnvVars[s.EnvVar] = "secrets." + name
		}
	}

	for key := range env {
		if !envVarPattern.MatchString(key) {
			errs = append(errs, fmt.Sprintf("env.%s name must match %s", key, envVarPattern))
			continue
		}
		if strings.HasPrefix(key, "RA_SECRET_") {
			errs = append(errs, fmt.Sprintf("env.%s collides with ra-reserved RA_SECRET_* prefix", key))
			continue
		}
		if strings.HasPrefix(key, "RA_PLUGIN_") {
			errs = append(errs, fmt.Sprintf("env.%s collides with ra-reserved RA_PLUGIN_* prefix", key))
			continue
		}
		if existing, collide := usedEnvVars[key]; collide {
			errs = append(errs, fmt.Sprintf("env.%s collides with %s", key, existing))
			continue
		}
		usedEnvVars[key] = "env." + key
	}

	return errs
}
