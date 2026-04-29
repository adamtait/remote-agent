package configwrite

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// helper: unmarshal and get nested string value safely
func getNestedString(t *testing.T, src, key1, key2, key3 string) string {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v1, _ := m[key1].(map[string]any)
	v2, _ := v1[key2].(map[string]any)
	s, _ := v2[key3].(string)
	return s
}

func TestMergePluginConfig_InsertIntoEmpty(t *testing.T) {
	original := `gcloud:
  project: my-project
`
	result, err := MergePluginConfig(
		original,
		"myplugin",
		map[string]any{"enabled": true, "region": "us-east1"},
		map[string]string{"my-secret": "gcp-secret-name"},
		map[string]string{"my-secret": "MY_SECRET_ENV"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// gcloud key must be preserved
	if !strings.Contains(result, "my-project") {
		t.Errorf("gcloud.project lost; got:\n%s", result)
	}

	// plugins.myplugin.enabled must be present
	if !strings.Contains(result, "myplugin") {
		t.Errorf("plugin name missing; got:\n%s", result)
	}
	if !strings.Contains(result, "enabled") {
		t.Errorf("enabled field missing; got:\n%s", result)
	}

	// secrets section
	if !strings.Contains(result, "my-secret") {
		t.Errorf("secret logical name missing; got:\n%s", result)
	}
	if !strings.Contains(result, "gcp-secret-name") {
		t.Errorf("GCP secret name missing; got:\n%s", result)
	}
	if !strings.Contains(result, "MY_SECRET_ENV") {
		t.Errorf("env var missing; got:\n%s", result)
	}
}

func TestMergePluginConfig_InsertIntoExistingSecrets(t *testing.T) {
	original := `secrets:
  existing-secret:
    secret_name: existing-gcp
    env_var: EXISTING_ENV
`
	result, err := MergePluginConfig(
		original,
		"newplugin",
		map[string]any{"key": "val"},
		map[string]string{"new-secret": "new-gcp"},
		map[string]string{"new-secret": "NEW_ENV"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Old secret must remain
	if !strings.Contains(result, "existing-secret") {
		t.Errorf("existing secret lost; got:\n%s", result)
	}
	if !strings.Contains(result, "existing-gcp") {
		t.Errorf("existing gcp name lost; got:\n%s", result)
	}

	// New secret must appear
	if !strings.Contains(result, "new-secret") {
		t.Errorf("new secret missing; got:\n%s", result)
	}
}

func TestMergeConfigName_InsertsIntoExistingWorkstationSection(t *testing.T) {
	original := `gcloud:
  project: my-project
  workstation:
    region: us-central1
`
	result, err := MergeConfigName(original, "remote-agent-config")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := getNestedString(t, result, "gcloud", "workstation", "config_name"); got != "remote-agent-config" {
		t.Errorf("config_name = %q, want remote-agent-config", got)
	}
	if !strings.Contains(result, "my-project") {
		t.Errorf("gcloud.project lost; got:\n%s", result)
	}
	if !strings.Contains(result, "us-central1") {
		t.Errorf("region lost; got:\n%s", result)
	}
}

func TestMergeConfigName_InsertsWithNoGcloudSection(t *testing.T) {
	original := `secrets:
  foo:
    secret_name: bar
    env_var: BAR
`
	result, err := MergeConfigName(original, "myrepo-config")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := getNestedString(t, result, "gcloud", "workstation", "config_name"); got != "myrepo-config" {
		t.Errorf("config_name = %q, want myrepo-config", got)
	}
	if !strings.Contains(result, "foo") {
		t.Errorf("existing secrets lost; got:\n%s", result)
	}
}

func TestMergeConfigName_Idempotent(t *testing.T) {
	original := `gcloud:
  project: p
`
	first, err := MergeConfigName(original, "my-config")
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	second, err := MergeConfigName(first, "my-config")
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if first != second {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMergeConfigName_OverwritesExisting(t *testing.T) {
	original := `gcloud:
  workstation:
    config_name: old-config
`
	result, err := MergeConfigName(original, "new-config")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := getNestedString(t, result, "gcloud", "workstation", "config_name"); got != "new-config" {
		t.Errorf("config_name = %q, want new-config", got)
	}
	if strings.Contains(result, "old-config") {
		t.Errorf("old config_name not replaced; got:\n%s", result)
	}
}

func TestMergePluginConfig_Idempotent(t *testing.T) {
	original := `gcloud:
  project: p
`
	first, err := MergePluginConfig(original, "myplugin",
		map[string]any{"enabled": true},
		map[string]string{"s": "gcp-s"},
		map[string]string{"s": "S_ENV"},
	)
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}

	second, err := MergePluginConfig(first, "myplugin",
		map[string]any{"enabled": true},
		map[string]string{"s": "gcp-s"},
		map[string]string{"s": "S_ENV"},
	)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}

	if first != second {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestRemovePluginConfig_RemovesEntries(t *testing.T) {
	original := `gcloud:
  project: p
plugins:
  myplugin:
    enabled: true
  other:
    enabled: false
secrets:
  my-secret:
    secret_name: gcp-s
    env_var: S_ENV
  keep-secret:
    secret_name: gcp-keep
    env_var: KEEP_ENV
`
	result, err := RemovePluginConfig(original, "myplugin", []string{"my-secret"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, "myplugin") {
		t.Errorf("myplugin still present; got:\n%s", result)
	}
	if strings.Contains(result, "my-secret") {
		t.Errorf("my-secret still present; got:\n%s", result)
	}

	// Preserved entries
	if !strings.Contains(result, "other") {
		t.Errorf("other plugin lost; got:\n%s", result)
	}
	if !strings.Contains(result, "keep-secret") {
		t.Errorf("keep-secret lost; got:\n%s", result)
	}
	if !strings.Contains(result, "my-project") || !strings.Contains(result, "p") {
		// project might be "p" — just check the key still round-trips
		if !strings.Contains(result, "gcloud") {
			t.Errorf("gcloud key lost; got:\n%s", result)
		}
	}
}

func TestMergePluginConfig_NullPluginsSection(t *testing.T) {
	original := `gcloud:
  project: x
plugins: ~
`
	result, err := MergePluginConfig(original, "p",
		map[string]any{"k": "v"},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "p:") {
		t.Errorf("plugin not inserted; got:\n%s", result)
	}
}
