// internal/plugins/schema.go
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
	Name        string      `yaml:"name"`
	Type        string      `yaml:"type"` // "bool", "string", "int"
	Description string      `yaml:"description"`
	Default     interface{} `yaml:"default"`
	Required    bool        `yaml:"required"`
	Options     []string    `yaml:"options"`
}

// SecretDef declares one GCP Secret Manager secret the plugin needs.
type SecretDef struct {
	Name              string `yaml:"name"`
	EnvVar            string `yaml:"env_var"`
	Description       string `yaml:"description"`
	DefaultSecretName string `yaml:"default_secret_name"`
}

// AuthProvider groups provider-specific secrets under a named auth method.
type AuthProvider struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Secrets     []SecretDef `yaml:"secrets"`
}
