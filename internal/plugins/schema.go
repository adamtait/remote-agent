package plugins

// PluginSchema is the parsed structure of a plugin.yaml file.
type PluginSchema struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	// PortForTunnel, when set, advertises the in-workstation TCP port the
	// plugin listens on and serves as the default rendered into the
	// `plugins.<name>.port-for-tunnel` config key at install time. The same
	// value is exported as RA_PLUGIN_<NAME>_PORT_FOR_TUNNEL so the plugin's
	// startup script can bind to it, and `ra tunnel <plugin-name>` reads
	// the configured value (falling back to this default) to pick a remote
	// port. 0 means the plugin opts out of named tunneling.
	PortForTunnel int            `yaml:"port-for-tunnel"`
	Config        []ConfigField  `yaml:"config"`
	Secrets       []SecretDef    `yaml:"secrets"`
	AuthProviders []AuthProvider `yaml:"auth_providers"`
	// Buckets declares per-plugin GCS bucket needs. When `enabled: true`
	// for the plugin, `ra create` provisions each bucket (idempotent),
	// binds the declared IAM role on it to the workstation service
	// account, and exports the resolved bucket name to the container via
	// the declared EnvVar. The mechanism in core is generic — no plugin
	// name is hard-coded — mirroring how `iam_roles:` on auth_providers
	// and `port-for-tunnel:` work.
	Buckets []BucketSpec `yaml:"buckets"`
}

// BucketSpec declares one GCS bucket the plugin needs at workstation
// create time. NameTemplate is resolved at `ra create` time against the
// active gcloud project and git user email; OverrideField, when set and
// non-empty in `plugins.<plugin>.<OverrideField>`, replaces the resolved
// template before validation.
type BucketSpec struct {
	Name          string `yaml:"name"`           // logical handle, e.g. "state"
	NameTemplate  string `yaml:"name_template"`  // supports {project_id}, {user_email_hash}
	OverrideField string `yaml:"override_field"` // optional plugin-config field that, if non-empty, replaces the template
	Role          string `yaml:"role"`           // IAM role to grant on the bucket to the workstation SA
	EnvVar        string `yaml:"env_var"`        // env var name exposed to the workstation container
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
