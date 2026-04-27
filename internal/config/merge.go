package config

import (
	"os"
	"os/user"
	"regexp"
	"strings"
)

// Merge combines user and project configs into a flat Config.
// Precedence: project > user. No hardcoded defaults; missing required
// fields are caught by Validate, not Merge.
func Merge(u *UserConfig, project *ProjectConfig) (*Config, error) {
	if u == nil {
		u = &UserConfig{}
	}
	if project == nil {
		project = &ProjectConfig{}
	}

	cfg := &Config{
		Git: GitConfig{
			UserName:  u.Git.UserName,
			UserEmail: u.Git.UserEmail,
		},
		Gcloud: GcloudConfig{
			Project: pick(project.Gcloud.Project, u.Gcloud.Project),
		},
	}

	// workstation: user is the baseline, project overrides per field.
	lift := func(ur, p WorkstationConfig) WorkstationConfig {
		return WorkstationConfig{
			Region:             pick(p.Region, ur.Region),
			MachineType:        pick(p.MachineType, ur.MachineType),
			DiskSizeGB:         pickInt(p.DiskSizeGB, ur.DiskSizeGB),
			ClusterName:        pick(p.ClusterName, ur.ClusterName),
			ConfigName:         pick(p.ConfigName, ur.ConfigName),
			ServiceAccountName: pick(p.ServiceAccountName, ur.ServiceAccountName),
			ImageName:          pick(p.ImageName, ur.ImageName),
			ImageTag:           pick(p.ImageTag, ur.ImageTag),
			RepoName:           pick(p.RepoName, ur.RepoName),
			AutoSleepHours:     pickIntPtr(p.AutoSleepHours, ur.AutoSleepHours),
			AutoShutdownHours:  pickInt(p.AutoShutdownHours, ur.AutoShutdownHours),
		}
	}
	cfg.Gcloud.Workstation = lift(u.Gcloud.Workstation, project.Gcloud.Workstation)

	cfg.Plugins = mergePluginsMap(u.Plugins, project.Plugins)

	// Secrets: user map seeds, project entries override or add by logical name.
	cfg.Secrets = mergeSecretMap(u.Secrets, project.Secrets)
	cfg.Env = mergeStringMap(u.Env, project.Env)

	cfg.WorkstationName = WorkstationName(cfg.Gcloud.Project, currentUsername())
	return cfg, nil
}

// pick returns p if non-empty, else u. Project-over-user for strings.
func pick(p, u string) string {
	if p != "" {
		return p
	}
	return u
}

// pickInt returns p if non-zero, else u. Project-over-user for ints.
func pickInt(p, u int) int {
	if p != 0 {
		return p
	}
	return u
}

// pickIntPtr: *int lets us distinguish "unset" (nil) from 0.
// Project override only applies if project's pointer is non-nil.
func pickIntPtr(p, u *int) *int {
	if p != nil {
		v := *p
		return &v
	}
	if u != nil {
		v := *u
		return &v
	}
	return nil
}

func mergeSecretMap(u, project map[string]SecretEntry) map[string]SecretEntry {
	out := map[string]SecretEntry{}
	for k, v := range u {
		out[k] = v
	}
	for k, v := range project {
		out[k] = v
	}
	return out
}

func mergeStringMap(u, project map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range u {
		out[k] = v
	}
	for k, v := range project {
		out[k] = v
	}
	return out
}

func mergePluginsMap(u, project map[string]map[string]interface{}) map[string]map[string]interface{} {
	out := make(map[string]map[string]interface{})
	for k, v := range u {
		out[k] = v
	}
	for k, v := range project {
		out[k] = v
	}
	return out
}

// currentUsername returns the username used to derive WorkstationName.
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "user"
}

var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9]+`)

// WorkstationName returns the default workstation name for a project/user pair.
func WorkstationName(projectID, username string) string {
	clean := func(s string) string {
		s = strings.ToLower(s)
		s = nonAlphanumHyphen.ReplaceAllString(s, "-")
		s = strings.Trim(s, "-")
		return s
	}
	name := clean(projectID) + "-" + clean(username)
	if len(name) > 63 {
		name = name[:63]
	}
	name = strings.TrimRight(name, "-")
	return name
}
