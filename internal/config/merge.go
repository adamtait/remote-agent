// ws/internal/config/merge.go
package config

import (
	"os"
	"os/user"
	"regexp"
	"strings"

	"github.com/adamtait/remote-agent/internal/scaffold"
	"gopkg.in/yaml.v3"
)

// LoadProjectConfig reads and parses a .ws.yaml file.
func LoadProjectConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Merge combines a project config and global config into a single Config,
// applying precedence: global project overrides > project config > global defaults > hardcoded defaults.
func Merge(project *ProjectConfig, global *GlobalConfig) (*Config, error) {
	cfg := &Config{}

	// Hardcoded defaults
	cfg.MachineType = "e2-standard-4"
	cfg.DiskSizeGB = 50
	cfg.ImageTag = "latest"
	cfg.ClusterName = "claude-dev-cluster"
	cfg.ConfigName = "claude-dev-config"
	cfg.ServiceAccountName = "claude-workstation-sa"
	cfg.ImageName = "claude-code-workstation"
	cfg.RepoName = "claude-workstation-images"

	// Global defaults
	if global.Defaults.Region != "" {
		cfg.Region = global.Defaults.Region
	}
	cfg.GitUserName = global.Defaults.GitUserName
	cfg.GitUserEmail = global.Defaults.GitUserEmail
	if global.Defaults.AutoSleepHours != nil {
		v := *global.Defaults.AutoSleepHours
		cfg.AutoSleepHours = &v
	}
	if global.Defaults.AutoShutdownHours != 0 {
		cfg.AutoShutdownHours = global.Defaults.AutoShutdownHours
	}

	// Project config
	if project.ProjectID != "" {
		cfg.ProjectID = project.ProjectID
	}
	if project.Region != "" {
		cfg.Region = project.Region
	}
	if project.ClusterName != "" {
		cfg.ClusterName = project.ClusterName
	}
	if project.ConfigName != "" {
		cfg.ConfigName = project.ConfigName
	}
	if project.GithubRepoURL != "" {
		cfg.GithubRepoURL = project.GithubRepoURL
	}
	if project.MachineType != "" {
		cfg.MachineType = project.MachineType
	}
	if project.DiskSizeGB != 0 {
		cfg.DiskSizeGB = project.DiskSizeGB
	}
	if project.AutoSleepHours != nil {
		v := *project.AutoSleepHours
		cfg.AutoSleepHours = &v
	}
	if project.AutoShutdownHours != 0 {
		cfg.AutoShutdownHours = project.AutoShutdownHours
	}

	// Global project overrides (highest precedence)
	if override, ok := global.Projects[cfg.ProjectID]; ok {
		if override.ServiceAccountName != "" {
			cfg.ServiceAccountName = override.ServiceAccountName
		}
		if override.ImageName != "" {
			cfg.ImageName = override.ImageName
		}
		if override.ImageTag != "" {
			cfg.ImageTag = override.ImageTag
		}
		if override.RepoName != "" {
			cfg.RepoName = override.RepoName
		}
		if override.AutoSleepHours != nil {
			v := *override.AutoSleepHours
			cfg.AutoSleepHours = &v
		}
		if override.AutoShutdownHours != 0 {
			cfg.AutoShutdownHours = override.AutoShutdownHours
		}
	}

	// Derive username for workstation name; os/user.Current is more reliable than $USER.
	var username string
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	} else {
		username = os.Getenv("USER")
	}
	if username == "" {
		username = "user"
	}

	// Workstation name is derived from the repo slug so a single GCP project
	// can host distinguishable workstations for multiple repos. Fall back to
	// the project ID if the repo URL is unset or unparseable (e.g. when a
	// command runs before .ws.yaml has been populated).
	slug := scaffold.RepoSlugFromURL(scaffold.NormalizeGithubRepoURL(cfg.GithubRepoURL))
	if slug == "" {
		slug = cfg.ProjectID
	}
	if slug == "" {
		slug = "ws"
	}
	cfg.WorkstationName = WorkstationName(slug, username)

	return cfg, nil
}

var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9]+`)

// WorkstationName returns the default workstation name for a repo/user pair.
// Input is sanitized to lowercase alphanumerics and hyphens and truncated to
// 63 characters (GCP Workstation name limit). Exported so it can be tested
// directly.
func WorkstationName(repoSlug, username string) string {
	clean := func(s string) string {
		s = strings.ToLower(s)
		s = nonAlphanumHyphen.ReplaceAllString(s, "-")
		s = strings.Trim(s, "-")
		return s
	}
	name := clean(repoSlug) + "-" + clean(username)
	if len(name) > 63 {
		name = name[:63]
	}
	name = strings.TrimRight(name, "-")
	return name
}
