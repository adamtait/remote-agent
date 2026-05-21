package plugins

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPluginSchemaParse(t *testing.T) {
	raw := `
name: testplugin
version: "1.0.0"
description: "A test plugin"
config:
  - name: enabled
    type: bool
    description: "Enable it?"
    default: false
    required: false
  - name: mode
    type: string
    description: "Mode"
    options: [fast, slow]
    required: true
secrets:
  - name: my_token
    env_var: MY_TOKEN
    description: "A token"
    default_secret_name: my-gcp-secret
auth_providers:
  - name: oauth
    description: "OAuth"
    secrets:
      - name: oauth_token
        env_var: OAUTH_TOKEN
        description: "OAuth token"
        default_secret_name: my-oauth-secret
        value_command:
          command: codex
          args: ["login"]
          output: file_base64
          path: "~/.codex/auth.json"
          validate: json
          interactive: true
  - name: apikey
    description: "API key"
    secrets:
      - name: api_key
        env_var: API_KEY
        description: "API key"
        default_secret_name: my-api-key
`
	var s PluginSchema
	if err := yaml.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Name != "testplugin" {
		t.Errorf("Name = %q, want %q", s.Name, "testplugin")
	}
	if len(s.Config) != 2 {
		t.Fatalf("len(Config) = %d, want 2", len(s.Config))
	}
	if s.Config[0].Name != "enabled" || s.Config[0].Type != "bool" {
		t.Errorf("Config[0] = %+v", s.Config[0])
	}
	if s.Config[0].Default != false {
		t.Errorf("Config[0].Default = %v, want false", s.Config[0].Default)
	}
	if s.Config[1].Required != true || len(s.Config[1].Options) != 2 {
		t.Errorf("Config[1] = %+v", s.Config[1])
	}
	if len(s.Secrets) != 1 || s.Secrets[0].EnvVar != "MY_TOKEN" {
		t.Errorf("Secrets = %+v", s.Secrets)
	}
	if len(s.AuthProviders) != 2 || s.AuthProviders[0].Name != "oauth" {
		t.Errorf("AuthProviders = %+v", s.AuthProviders)
	}
	if s.AuthProviders[0].Secrets[0].DefaultSecretName != "my-oauth-secret" {
		t.Errorf("AuthProviders[0].Secrets[0].DefaultSecretName = %q", s.AuthProviders[0].Secrets[0].DefaultSecretName)
	}
	// IAMRoles is optional; absence should yield a nil/empty slice.
	if len(s.AuthProviders[0].IAMRoles) != 0 {
		t.Errorf("AuthProviders[0].IAMRoles should be empty, got %v", s.AuthProviders[0].IAMRoles)
	}
	cmd := s.AuthProviders[0].Secrets[0].ValueCommand
	if cmd == nil {
		t.Fatal("expected oauth_token value_command")
	}
	if cmd.Command != "codex" || cmd.Output != "file_base64" || cmd.Path != "~/.codex/auth.json" {
		t.Errorf("unexpected value_command: %+v", cmd)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "login" {
		t.Errorf("unexpected value_command args: %+v", cmd.Args)
	}
	if cmd.Validate != "json" || !cmd.Interactive {
		t.Errorf("unexpected value_command validation/interactive: %+v", cmd)
	}
}

// TestPluginSchemaParse_IAMRolesOnAuthProvider confirms that an
// auth_provider with no secrets but with iam_roles parses correctly —
// the shape used when a provider grants access via the workstation's
// service account instead of a fetched secret.
func TestPluginSchemaParse_IAMRolesOnAuthProvider(t *testing.T) {
	raw := `
name: example
version: "1.0.0"
description: "Example plugin"
auth_providers:
  - name: vertex
    description: "Vertex AI"
    secrets: []
    iam_roles:
      - roles/aiplatform.user
      - roles/serviceusage.serviceUsageConsumer
`
	var s PluginSchema
	if err := yaml.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(s.AuthProviders) != 1 {
		t.Fatalf("len(AuthProviders) = %d, want 1", len(s.AuthProviders))
	}
	got := s.AuthProviders[0].IAMRoles
	want := []string{"roles/aiplatform.user", "roles/serviceusage.serviceUsageConsumer"}
	if len(got) != len(want) {
		t.Fatalf("IAMRoles len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("IAMRoles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(s.AuthProviders[0].Secrets) != 0 {
		t.Errorf("Secrets should be empty for vertex provider, got %v", s.AuthProviders[0].Secrets)
	}
}

// TestPluginSchemaParse_Buckets confirms the generic plugin-buckets hook
// parses fields correctly. The mechanism in cmd/create.go consumes this
// shape directly — same pattern as iam_roles on auth_providers.
func TestPluginSchemaParse_Buckets(t *testing.T) {
	raw := `
name: example
version: "1.0.0"
description: "Example plugin"
buckets:
  - name: state
    name_template: "ra-example-{project_id}-{user_email_hash}"
    override_field: bucket_name_override
    role: roles/storage.objectAdmin
    env_var: RA_PLUGIN_EXAMPLE_BUCKET
  - name: cache
    name_template: "ra-example-cache-{project_id}"
    role: roles/storage.objectViewer
    env_var: RA_PLUGIN_EXAMPLE_CACHE_BUCKET
`
	var s PluginSchema
	if err := yaml.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(s.Buckets) != 2 {
		t.Fatalf("len(Buckets) = %d, want 2", len(s.Buckets))
	}
	if s.Buckets[0].Name != "state" || s.Buckets[0].NameTemplate != "ra-example-{project_id}-{user_email_hash}" {
		t.Errorf("Buckets[0] parsed wrong: %+v", s.Buckets[0])
	}
	if s.Buckets[0].OverrideField != "bucket_name_override" {
		t.Errorf("Buckets[0].OverrideField = %q, want bucket_name_override", s.Buckets[0].OverrideField)
	}
	if s.Buckets[0].Role != "roles/storage.objectAdmin" || s.Buckets[0].EnvVar != "RA_PLUGIN_EXAMPLE_BUCKET" {
		t.Errorf("Buckets[0] role/env_var wrong: %+v", s.Buckets[0])
	}
	if s.Buckets[1].OverrideField != "" {
		t.Errorf("Buckets[1].OverrideField should be empty, got %q", s.Buckets[1].OverrideField)
	}
}
