package config

// ProjectConfig is unmarshalled from .ws.yaml.
type ProjectConfig struct {
	ProjectID          string `yaml:"project_id"`
	Region             string `yaml:"region"`
	ClusterName        string `yaml:"cluster_name"`
	ConfigName         string `yaml:"config_name"`
	GithubRepoURL      string `yaml:"github_repo_url"`
	MachineType        string `yaml:"machine_type"`
	DiskSizeGB         int    `yaml:"disk_size_gb"`
	AutoSleepHours    *int `yaml:"auto_sleep_hours"`
	AutoShutdownHours int  `yaml:"auto_shutdown_hours"`
}

// GlobalDefaults is the defaults section of ~/.config/ws/config.yaml.
type GlobalDefaults struct {
	Region              string `yaml:"region"`
	GitUserName         string `yaml:"git_user_name"`
	GitUserEmail        string `yaml:"git_user_email"`
	AutoSleepHours    *int `yaml:"auto_sleep_hours"`
	AutoShutdownHours int  `yaml:"auto_shutdown_hours"`
}

// GlobalProjectOverride is a per-project-id override block in the global config.
type GlobalProjectOverride struct {
	ServiceAccountName  string `yaml:"service_account_name"`
	ImageName           string `yaml:"image_name"`
	ImageTag            string `yaml:"image_tag"`
	RepoName            string `yaml:"repo_name"`
	AutoSleepHours    *int `yaml:"auto_sleep_hours"`
	AutoShutdownHours int  `yaml:"auto_shutdown_hours"`
}

// GlobalConfig is unmarshalled from ~/.config/ws/config.yaml.
type GlobalConfig struct {
	Defaults GlobalDefaults                   `yaml:"defaults"`
	Projects map[string]GlobalProjectOverride `yaml:"projects"`
}

// Config is the fully merged, validated configuration passed to commands.
type Config struct {
	ProjectID           string
	Region              string
	ClusterName         string
	ConfigName          string
	GithubRepoURL       string
	MachineType         string
	DiskSizeGB          int
	GitUserName         string
	GitUserEmail        string
	ServiceAccountName  string
	ImageName           string
	ImageTag            string
	RepoName            string
	WorkstationName     string // <project_id>-<username>, sanitized, ≤63 chars
	ProjectConfigPath   string // absolute path to the .ws.yaml that was found
	AutoSleepHours    *int // nil=GCP default, 0=never sleep, N=hours (1-24)
	AutoShutdownHours int  // 0=GCP default, N=hours (1-168)
}
