package config

// ProjectConfig is unmarshalled from .workstation.yaml.
type ProjectConfig struct {
	ProjectID     string `yaml:"project_id"`
	Region        string `yaml:"region"`
	ClusterName   string `yaml:"cluster_name"`
	ConfigName    string `yaml:"config_name"`
	GithubRepoURL string `yaml:"github_repo_url"`
	MachineType   string `yaml:"machine_type"`
	DiskSizeGB    int    `yaml:"disk_size_gb"`
}

// GlobalDefaults is the defaults section of ~/.config/cws/config.yaml.
type GlobalDefaults struct {
	Region       string `yaml:"region"`
	GitUserName  string `yaml:"git_user_name"`
	GitUserEmail string `yaml:"git_user_email"`
}

// GlobalProjectOverride is a per-project-id override block in the global config.
type GlobalProjectOverride struct {
	ServiceAccountName string `yaml:"service_account_name"`
	ImageName          string `yaml:"image_name"`
	ImageTag           string `yaml:"image_tag"`
	RepoName           string `yaml:"repo_name"`
}

// GlobalConfig is unmarshalled from ~/.config/cws/config.yaml.
type GlobalConfig struct {
	Defaults GlobalDefaults                   `yaml:"defaults"`
	Projects map[string]GlobalProjectOverride `yaml:"projects"`
}

// Config is the fully merged, validated configuration passed to commands.
type Config struct {
	ProjectID          string
	Region             string
	ClusterName        string
	ConfigName         string
	GithubRepoURL      string
	MachineType        string
	DiskSizeGB         int
	GitUserName        string
	GitUserEmail       string
	ServiceAccountName string
	ImageName          string
	ImageTag           string
	RepoName           string
	WorkstationName    string // <project_id>-<username>, sanitized, ≤63 chars
	ProjectConfigPath  string // absolute path to the .workstation.yaml that was found
}
