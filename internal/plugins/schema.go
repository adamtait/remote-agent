package plugins

// PluginSchema is the parsed structure of a plugin.yaml file.
type PluginSchema struct {
	Name          string         `yaml:"name"`
	Version       string         `yaml:"version"`
	Description   string         `yaml:"description"`
	Config        []ConfigField  `yaml:"config"`
	Secrets       []SecretDef    `yaml:"secrets"`
	AuthProviders []AuthProvider `yaml:"auth_providers"`
}

// ConfigField declares one user-facing config field.
type ConfigField struct {
	Name         string        `yaml:"name"`
	Type         string        `yaml:"type"` // "bool", "string", "int", "string_list"
	Description  string        `yaml:"description"`
	Default      any           `yaml:"default"`
	Required     bool          `yaml:"required"`
	Options      []string      `yaml:"options"`
	ValueCommand *ValueCommand `yaml:"value_command"`
}

// SecretDef declares one GCP Secret Manager secret the plugin needs.
type SecretDef struct {
	Name              string        `yaml:"name"`
	EnvVar            string        `yaml:"env_var"`
	Description       string        `yaml:"description"`
	DefaultSecretName string        `yaml:"default_secret_name"`
	ValueCommand      *ValueCommand `yaml:"value_command"`
}

// ValueCommand declares an install-time local command that produces a plugin
// config value or secret value.
type ValueCommand struct {
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args"`
	Output      string   `yaml:"output"` // "stdout" or "file_base64"
	Path        string   `yaml:"path"`
	Validate    string   `yaml:"validate"` // currently "json" or empty
	Interactive bool     `yaml:"interactive"`
	Description string   `yaml:"description"`
}

// AuthProvider groups provider-specific secrets under a named auth method.
// IAMRoles lists project-level GCP IAM roles to grant the workstation service
// account when this provider is the selected auth_provider for the plugin —
// e.g. roles/aiplatform.user when a plugin authenticates via Vertex AI using
// the workstation's existing service account instead of a stored secret.
type AuthProvider struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Secrets     []SecretDef `yaml:"secrets"`
	IAMRoles    []string    `yaml:"iam_roles"`
}
