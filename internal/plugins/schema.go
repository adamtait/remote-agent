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
	PortForTunnel int `yaml:"port-for-tunnel"`
	// Commands declares CLI subcommands the plugin contributes, invoked as
	// `ra plugin <plugin-name> <command-name>`. Each runs a local script
	// (`run:`, relative to the plugin dir) with a documented RA_* env contract;
	// ra resolves + ensures the workstation running first when
	// `needs_workstation: true`. Generic, like `buckets:`/`port-for-tunnel:`:
	// core hard-codes no plugin name. See docs/adr/0014.
	Commands []CommandSpec `yaml:"commands"`
	// PostInstallMessage, when set, is printed by `ra plugin install` after a
	// successful install — e.g. to recommend a config change the plugin can't
	// (or shouldn't) make itself. Purely informational.
	PostInstallMessage string         `yaml:"post_install_message"`
	Config             []ConfigField  `yaml:"config"`
	Secrets            []SecretDef    `yaml:"secrets"`
	AuthProviders      []AuthProvider `yaml:"auth_providers"`
	// Buckets declares per-plugin GCS bucket needs. When `enabled: true`
	// for the plugin, `ra create` provisions each bucket (idempotent),
	// binds the declared IAM role on it to the workstation service
	// account, and exports the resolved bucket name to the container via
	// the declared EnvVar. The mechanism in core is generic — no plugin
	// name is hard-coded — mirroring how `iam_roles:` on auth_providers
	// and `port-for-tunnel:` work.
	Buckets []BucketSpec `yaml:"buckets"`
	// StableID, when set, asks `ra create` to inject a deterministic
	// workstation-identity token into the named env var. The token is
	// derived from (gcloud project, workstation config_name, workstation
	// name, git user email), so it is stable across `ra create` rebuilds
	// and distinct per workstation on a shared config — unlike the GCE
	// instance name, which is a fresh UUID each create. Generic, like
	// `buckets:` and `port-for-tunnel:`: core hard-codes no plugin name;
	// the env-var name comes from this declaration, and injection is gated
	// on the plugin being `enabled: true`. A plugin keyed on this token
	// reuses one per-workstation identity across rebuilds instead of
	// accumulating a fresh one each time.
	StableID *StableIDSpec `yaml:"stable_id"`
}

// StableIDSpec declares that the plugin wants a deterministic,
// rebuild-stable workstation-identity token exposed to the container.
// EnvVar is the env var name the token is injected under. See
// PluginSchema.StableID.
type StableIDSpec struct {
	EnvVar string `yaml:"env_var"`
}

// CommandSpec declares one plugin-contributed CLI subcommand, invoked as
// `ra plugin <plugin-name> <Name>`. Run is a script path relative to the plugin
// directory, executed locally with the RA_* env contract wired in. When
// NeedsWorkstation is true, ra resolves the target workstation and ensures it is
// running before exec, and sets RA_WORKSTATION/RA_PROJECT/etc. accordingly.
type CommandSpec struct {
	Name             string `yaml:"name"`
	Description      string `yaml:"description"`
	Run              string `yaml:"run"`
	NeedsWorkstation bool   `yaml:"needs_workstation"`
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
	// Lifecycle, when non-empty, declares GCS Object Lifecycle Management
	// rules `ra create` applies to the bucket after ensuring it exists.
	// Generic, like the rest of BucketSpec — core builds the lifecycle
	// JSON and shells out; the policy (ages, classes, prefixes) lives in
	// plugin.yaml. A plugin can scope rules to its own object-name prefix
	// so they never touch another plugin's data in a shared bucket, and
	// thereby keep the bucket's at-rest cost bounded.
	Lifecycle []LifecycleRule `yaml:"lifecycle"`
	// SoftDeleteDays, when set, asserts the bucket's GCS soft-delete
	// retention: overwritten or deleted objects stay restorable (`gcloud
	// storage restore`) for this many days. GCS accepts 0 (disabled) or
	// 7–90. When several plugins share a bucket, `ra create` applies the
	// largest declared value so no plugin's recovery window shrinks. Nil
	// leaves the bucket's existing policy alone.
	SoftDeleteDays *int `yaml:"soft_delete_days"`
}

// LifecycleRule is one GCS Object Lifecycle Management rule, mapped 1:1
// onto the JSON `gcloud storage buckets update --lifecycle-file` consumes.
// Action is "Delete" or "SetStorageClass"; StorageClass is required only
// for "SetStorageClass". The condition fields cover the subset ra needs:
// age in days plus an object-name prefix match, so a rule can be scoped to
// just the plugin's own prefix inside a bucket shared by several plugins.
type LifecycleRule struct {
	Action       string   `yaml:"action"`        // "Delete" | "SetStorageClass"
	StorageClass string   `yaml:"storage_class"` // e.g. "COLDLINE" (required for SetStorageClass)
	AgeDays      int      `yaml:"age_days"`      // condition: object age in days
	Prefixes     []string `yaml:"prefixes"`      // condition: matchesPrefix (empty = whole bucket)
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
