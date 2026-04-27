package config

type GitConfig struct {
	UserName  string `yaml:"user_name"`
	UserEmail string `yaml:"user_email"`
}

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

type SecretEntry struct {
	SecretName string `yaml:"secret_name"`
	EnvVar     string `yaml:"env_var"`
}

type GcloudConfig struct {
	Project     string            `yaml:"project"`
	Workstation WorkstationConfig `yaml:"workstation"`
}

type UserConfig struct {
	Git     GitConfig                         `yaml:"git"`
	Gcloud  GcloudConfig                      `yaml:"gcloud"`
	Plugins map[string]map[string]interface{} `yaml:"plugins,omitempty"`
	Secrets map[string]SecretEntry            `yaml:"secrets"`
	Env     map[string]string                 `yaml:"env"`
}

type ProjectConfig struct {
	Git     GitConfig                         `yaml:"git"`
	Gcloud  GcloudConfig                      `yaml:"gcloud"`
	Plugins map[string]map[string]interface{} `yaml:"plugins,omitempty"`
	Secrets map[string]SecretEntry            `yaml:"secrets"`
	Env     map[string]string                 `yaml:"env"`
}

type Config struct {
	Git    GitConfig
	Gcloud GcloudConfig

	Plugins map[string]map[string]interface{}

	Secrets map[string]SecretEntry
	Env     map[string]string

	WorkstationName   string
	ProjectConfigPath string
}
