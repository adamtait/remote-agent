package config

// GitConfig is the "git:" section of ~/.config/ws/config.yaml.
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

// UserConfig is ~/.config/ws/config.yaml — the exhaustive baseline.
type UserConfig struct {
	Git     GitConfig              `yaml:"git"`
	Gcloud  GcloudConfig           `yaml:"gcloud"`
	Secrets map[string]SecretEntry `yaml:"secrets"`
	Env     map[string]string      `yaml:"env"`
}

// ProjectConfig is .ws/config.yaml — required project keys + overrides.
type ProjectConfig struct {
	Git     GitConfig              `yaml:"git"`
	Gcloud  GcloudConfig           `yaml:"gcloud"`
	Secrets map[string]SecretEntry `yaml:"secrets"`
	Env     map[string]string      `yaml:"env"`
}

// Config is the post-merge configuration consumed by every ws command.
type Config struct {
	Git    GitConfig
	Gcloud GcloudConfig

	Secrets map[string]SecretEntry
	Env     map[string]string

	WorkstationName   string // <project_id>-<username>, sanitized, ≤63 chars
	ProjectConfigPath string // absolute path to the .ws/config.yaml that was found
}
