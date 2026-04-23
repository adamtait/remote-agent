package config

// GitConfig is the "git:" section of ~/.config/ws/config.yaml.
type GitConfig struct {
	UserName  string `yaml:"user_name"`
	UserEmail string `yaml:"user_email"`
}

// ProjectSection is the "project:" section of .ws/config.yaml.
type ProjectSection struct {
	ID            string `yaml:"id"`
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

// UserConfig is ~/.config/ws/config.yaml — the exhaustive baseline.
type UserConfig struct {
	Git         GitConfig              `yaml:"git"`
	Workstation WorkstationConfig      `yaml:"workstation"`
	Secrets     map[string]SecretEntry `yaml:"secrets"`
	Env         map[string]string      `yaml:"env"`
}

// ProjectConfig is .ws/config.yaml — required project keys + overrides.
type ProjectConfig struct {
	Project     ProjectSection         `yaml:"project"`
	Workstation WorkstationConfig      `yaml:"workstation"`
	Secrets     map[string]SecretEntry `yaml:"secrets"`
	Env         map[string]string      `yaml:"env"`
}

// Config is the post-merge, flat configuration consumed by every ws command.
// Merge lifts the grouped inputs into these flat fields.
type Config struct {
	ProjectID     string
	GithubRepoURL string

	GitUserName  string
	GitUserEmail string

	Region             string
	MachineType        string
	DiskSizeGB         int
	ClusterName        string
	ConfigName         string
	ServiceAccountName string
	ImageName          string
	ImageTag           string
	RepoName           string
	AutoSleepHours     *int
	AutoShutdownHours  int

	Secrets map[string]SecretEntry
	Env     map[string]string

	WorkstationName   string // <project_id>-<username>, sanitized, ≤63 chars
	ProjectConfigPath string // absolute path to the .ws/config.yaml that was found
}
