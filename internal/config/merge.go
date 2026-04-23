// ws/internal/config/merge.go
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
		// project-only fields
		ProjectID:     project.Project.ID,
		GithubRepoURL: project.Project.GithubRepoURL,

		// git — user only (never read from project)
		GitUserName:  u.Git.UserName,
		GitUserEmail: u.Git.UserEmail,
	}

	// workstation: user is the baseline, project overrides per field.
	lift := func(ur, p WorkstationConfig) {
		cfg.Region = pick(p.Region, ur.Region)
		cfg.MachineType = pick(p.MachineType, ur.MachineType)
		cfg.DiskSizeGB = pickInt(p.DiskSizeGB, ur.DiskSizeGB)
		cfg.ClusterName = pick(p.ClusterName, ur.ClusterName)
		cfg.ConfigName = pick(p.ConfigName, ur.ConfigName)
		cfg.ServiceAccountName = pick(p.ServiceAccountName, ur.ServiceAccountName)
		cfg.ImageName = pick(p.ImageName, ur.ImageName)
		cfg.ImageTag = pick(p.ImageTag, ur.ImageTag)
		cfg.RepoName = pick(p.RepoName, ur.RepoName)
		cfg.AutoSleepHours = pickIntPtr(p.AutoSleepHours, ur.AutoSleepHours)
		cfg.AutoShutdownHours = pickInt(p.AutoShutdownHours, ur.AutoShutdownHours)
	}
	lift(u.Workstation, project.Workstation)

	// Secrets: user map seeds, project entries override or add by logical name.
	cfg.Secrets = mergeSecretMap(u.Secrets, project.Secrets)
	cfg.Env = mergeStringMap(u.Env, project.Env)

	cfg.WorkstationName = WorkstationName(cfg.ProjectID, currentUsername())
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
