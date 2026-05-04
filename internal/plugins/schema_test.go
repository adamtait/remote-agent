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
