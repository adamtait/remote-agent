package config

// GitConfig is the "git:" section of ~/.config/ra/config.yaml.
type GitConfig struct {
	UserName      string `yaml:"user_name"`
	UserEmail     string `yaml:"user_email"`
	GithubRepoURL string `yaml:"github_repo_url"`
}

// WorkstationConfig is the "workstation:" section, present in both files.
type WorkstationConfig struct {
	Region             string `yaml:"region"`
	MachineType        string `yaml:"machine_type"`
	DiskSizeGB         int    `yaml:"disk_size_gb"`
	ClusterName        string `yaml:"cluster_name"`
	ConfigName         string `yaml:"config_name"`
	ServiceAccountName string `yaml:"service_account_name"`
	ImageName          string `yaml:"image_name"`
	ImageTag           string `yaml:"image_tag"`
	RepoName           string `yaml:"repo_name"`
	AutoSleepHours     *int   `yaml:"auto_sleep_hours"`
	AutoShutdownHours  int    `yaml:"auto_shutdown_hours"`
}

// SecretEntry is one row of the "secrets:" map. Logical name is the map key.
type SecretEntry struct {
	SecretName             string `yaml:"secret_name"`
	EnvVar                 string `yaml:"env_var"`
	OAuthAccountSecretName string `yaml:"oauth_account_secret_name,omitempty"`
}

// GcloudConfig is the "gcloud:" section of config.yaml.
type GcloudConfig struct {
	Project     string            `yaml:"project"`
	Workstation WorkstationConfig `yaml:"workstation"`
}

// ClaudeConfig is the "claude:" section. Enabled is a pointer so Merge can
// distinguish unset (nil) from an explicit `enabled: false` — matching the
// *int pattern used for AutoSleepHours.
type ClaudeConfig struct {
	Enabled  *bool  `yaml:"enabled,omitempty"`
	AuthMode string `yaml:"auth_mode,omitempty"`
}

// IsEnabled returns the resolved enabled state; nil pointer means disabled.
func (c ClaudeConfig) IsEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}

// UserConfig is ~/.config/ra/config.yaml — the exhaustive baseline.
type UserConfig struct {
	Git     GitConfig              `yaml:"git"`
	Gcloud  GcloudConfig           `yaml:"gcloud"`
	Claude  ClaudeConfig           `yaml:"claude"`
	Secrets map[string]SecretEntry `yaml:"secrets"`
	Env     map[string]string      `yaml:"env"`
}

// ProjectConfig is .ra/config.yaml — required project keys + overrides.
type ProjectConfig struct {
	Git     GitConfig              `yaml:"git"`
	Gcloud  GcloudConfig           `yaml:"gcloud"`
	Claude  ClaudeConfig           `yaml:"claude"`
	Secrets map[string]SecretEntry `yaml:"secrets"`
	Env     map[string]string      `yaml:"env"`
}

// Config is the post-merge configuration consumed by every ws command.
type Config struct {
	Git    GitConfig
	Gcloud GcloudConfig
	Claude ClaudeConfig

	Secrets map[string]SecretEntry
	Env     map[string]string

	WorkstationName   string // <project_id>-<username>, sanitized, ≤63 chars
	ProjectConfigPath string // absolute path to the .ra/config.yaml that was found
}
