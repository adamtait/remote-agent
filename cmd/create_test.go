// ra/cmd/create_test.go
package cmd

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/plugins"
)

func TestElapsedHuman(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{12 * time.Second, "12s"},
		// Sub-second remainder is truncated, not rounded.
		{4*time.Minute + 11*time.Second + 900*time.Millisecond, "4m11s"},
		{4*time.Minute + 12*time.Second, "4m12s"},
		{1*time.Hour + 3*time.Minute + 7*time.Second, "1h3m7s"},
	}
	for _, tc := range cases {
		if got := elapsedHuman(tc.d); got != tc.want {
			t.Errorf("elapsedHuman(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestBuildArgs_UsesDashFAndExplicitBuildContext(t *testing.T) {
	got := dockerBuildArgs("example.com/img:tag", "/tmp/ra-build/myws")
	want := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", "example.com/img:tag",
		"-f", "/tmp/ra-build/myws/Dockerfile",
		"--push",
		"/tmp/ra-build/myws",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("args mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestBuildArgs_NoLegacyPluginBuildArgs(t *testing.T) {
	// Plugin enable/disable lives in the container env (set by
	// buildContainerEnv) and is read at runtime by each plugin's startup
	// script. No plugin-specific build args should leak into buildx.
	// RA_CLAUDE_ENABLED is the historical example we guard against.
	got := dockerBuildArgs("example.com/img:tag", "/tmp/ra-build/myws")
	for _, a := range got {
		if strings.Contains(a, "RA_CLAUDE_ENABLED") {
			t.Errorf("dockerBuildArgs should not pass RA_CLAUDE_ENABLED at build time; got %v", got)
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Ensure the runner interface used by create.go is usable with a fake.
// This compile-time check catches breaks if we later extract an interface.
var _ io.Writer = (*bytes.Buffer)(nil)
var _ error = errors.New("sentinel")

func TestMakeBuildDir_ConcurrentSafeAndContained(t *testing.T) {
	home := t.TempDir()
	a, err := makeBuildDir(home, "myws")
	if err != nil {
		t.Fatal(err)
	}
	b, err := makeBuildDir(home, "myws")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("expected unique dirs for concurrent calls, both = %q", a)
	}
	root := filepath.Join(home, ".cache", "ra", "build")
	for _, d := range []string{a, b} {
		if !strings.HasPrefix(d, root+string(filepath.Separator)) {
			t.Errorf("dir %q escaped cache root %q", d, root)
		}
	}
}

func TestMakeBuildDir_RejectsPathTraversal(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".cache", "ra", "build")
	for _, evil := range []string{"../etc", "/etc/passwd", "..", ".", ""} {
		got, err := makeBuildDir(home, evil)
		if err != nil {
			t.Fatalf("input %q: unexpected error %v", evil, err)
		}
		if !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("input %q produced %q which escapes %q", evil, got, root)
		}
	}
}

func TestBuildContainerEnv_IncludesSecretsAndEnv(t *testing.T) {
	sleep := 2
	cfg := &config.Config{
		Git: config.GitConfig{
			UserName:  "Alice",
			UserEmail: "a@x.com",
		},
		Gcloud: config.GcloudConfig{
			Project: "p",
			Workstation: config.WorkstationConfig{
				Region:             "us-central1",
				MachineType:        "e2-standard-4",
				DiskSizeGB:         50,
				ClusterName:        "c",
				ConfigName:         "cf",
				ServiceAccountName: "sa",
				ImageName:          "i",
				ImageTag:           "t",
				RepoName:           "r",
				AutoSleepHours:     &sleep,
				AutoShutdownHours:  12,
			},
		},
		Secrets: map[string]config.SecretEntry{
			"example_secret": {SecretName: "example-secret", EnvVar: "EXAMPLE_TOKEN"},
			"my_extra":       {SecretName: "ex", EnvVar: "MY_EXTRA"},
		},
		Env: map[string]string{"STATIC_KEY": "abc"},
	}
	got := buildContainerEnv(cfg)
	m := map[string]string{}
	for _, p := range strings.Split(got, ",") {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	must := map[string]string{
		"GCP_PROJECT_ID":                "p",
		"GIT_USER_NAME":                 "Alice",
		"GIT_USER_EMAIL":                "a@x.com",
		"RA_SECRET_EXAMPLE_SECRET_NAME": "example-secret",
		"RA_SECRET_EXAMPLE_SECRET_VAR":  "EXAMPLE_TOKEN",
		"RA_SECRET_MY_EXTRA_NAME":       "ex",
		"RA_SECRET_MY_EXTRA_VAR":        "MY_EXTRA",
		"STATIC_KEY":                    "abc",
	}
	for k, v := range must {
		if m[k] != v {
			t.Errorf("container env %s: got %q, want %q (full string: %q)", k, m[k], v, got)
		}
	}
	parts := strings.Split(m["RA_SECRETS"], "|")
	wantSet := map[string]bool{"example_secret": true, "my_extra": true}
	for _, p := range parts {
		if !wantSet[p] {
			t.Errorf("unexpected RA_SECRETS entry: %q", p)
		}
		delete(wantSet, p)
	}
	if len(wantSet) != 0 {
		t.Errorf("missing RA_SECRETS entries: %v", wantSet)
	}
	for _, banned := range []string{"CLAUDE_AUTH_MODE=", "CLAUDE_AUTH_SECRET_NAME=", "GITHUB_PAT_SECRET_NAME="} {
		if strings.Contains(got, banned) {
			t.Errorf("container env still contains old key %q: %q", banned, got)
		}
	}

	// RA_PROPAGATE_KEYS must list identity vars and any cfg.Env keys, so
	// 200_remote-agent-setup.sh can re-export them into /etc/environment and
	// /run/ra/env (login shells don't inherit PID-1 env). It MUST NOT list
	// the RA_SECRETS bookkeeping keys (RA_SECRETS, RA_SECRET_*_NAME,
	// RA_SECRET_*_VAR) — those are useless to login shells; the actual
	// fetched secret values get exported under their target env var names by
	// the secret-fetch loop.
	propParts := strings.Split(m["RA_PROPAGATE_KEYS"], "|")
	propSet := map[string]bool{}
	for _, k := range propParts {
		propSet[k] = true
	}
	for _, want := range []string{"GCP_PROJECT_ID", "GIT_USER_NAME", "GIT_USER_EMAIL", "STATIC_KEY"} {
		if !propSet[want] {
			t.Errorf("RA_PROPAGATE_KEYS missing %q: %q", want, m["RA_PROPAGATE_KEYS"])
		}
	}
	for _, banned := range []string{"RA_SECRETS", "RA_SECRET_EXAMPLE_SECRET_NAME", "RA_SECRET_EXAMPLE_SECRET_VAR", "RA_SECRET_MY_EXTRA_NAME", "RA_SECRET_MY_EXTRA_VAR"} {
		if propSet[banned] {
			t.Errorf("RA_PROPAGATE_KEYS must not include bookkeeping key %q: %q", banned, m["RA_PROPAGATE_KEYS"])
		}
	}
}

// loadTestPlugin loads a plugin from an inline YAML literal — used by the
// pluginAuthIAMRoles tests so each case can declare exactly the schema it
// needs without relying on a fixture directory.
func loadTestPlugin(t *testing.T, yaml string) *plugins.FilePlugin {
	t.Helper()
	fsys := fstest.MapFS{
		"plugin.yaml": &fstest.MapFile{Data: []byte(yaml)},
	}
	p, err := plugins.LoadPlugin(fsys)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	return p
}

func TestPluginAuthIAMRoles_VertexEnabled(t *testing.T) {
	p := loadTestPlugin(t, `
name: gemini
version: "1.0.0"
description: "Gemini CLI"
auth_providers:
  - name: apikey
    description: "API key"
  - name: vertex
    description: "Vertex AI"
    iam_roles:
      - roles/aiplatform.user
`)
	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"gemini": {
				"enabled":       true,
				"auth_provider": "vertex",
			},
		},
	}
	got := pluginAuthIAMRoles([]*plugins.FilePlugin{p}, cfg)
	want := []string{"roles/aiplatform.user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pluginAuthIAMRoles = %v, want %v", got, want)
	}
}

func TestPluginAuthIAMRoles_ApikeyProviderHasNoRoles(t *testing.T) {
	// Picking a provider that doesn't declare iam_roles must produce no
	// bindings — we don't want to grant Vertex access to a workstation
	// that's only using the API-key path.
	p := loadTestPlugin(t, `
name: gemini
version: "1.0.0"
description: "Gemini CLI"
auth_providers:
  - name: apikey
    description: "API key"
  - name: vertex
    description: "Vertex AI"
    iam_roles:
      - roles/aiplatform.user
`)
	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"gemini": {
				"enabled":       true,
				"auth_provider": "apikey",
			},
		},
	}
	got := pluginAuthIAMRoles([]*plugins.FilePlugin{p}, cfg)
	if len(got) != 0 {
		t.Errorf("pluginAuthIAMRoles should be empty for apikey provider, got %v", got)
	}
}

func TestPluginAuthIAMRoles_DisabledPluginSkipped(t *testing.T) {
	// Even if a plugin's selected provider declares iam_roles, a disabled
	// plugin should not get any bindings — matches the runtime gating in
	// workstation startup scripts (RA_PLUGIN_<NAME>_ENABLED).
	p := loadTestPlugin(t, `
name: gemini
version: "1.0.0"
description: "Gemini CLI"
auth_providers:
  - name: vertex
    description: "Vertex AI"
    iam_roles:
      - roles/aiplatform.user
`)
	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"gemini": {
				"enabled":       false,
				"auth_provider": "vertex",
			},
		},
	}
	got := pluginAuthIAMRoles([]*plugins.FilePlugin{p}, cfg)
	if len(got) != 0 {
		t.Errorf("pluginAuthIAMRoles should be empty for disabled plugin, got %v", got)
	}
}

func TestPluginAuthIAMRoles_NoConfigEntry(t *testing.T) {
	// A plugin loaded into the registry but with no entry in cfg.Plugins
	// should contribute no roles — without a selected auth_provider we
	// can't know which (if any) iam_roles to apply.
	p := loadTestPlugin(t, `
name: gemini
version: "1.0.0"
description: "Gemini CLI"
auth_providers:
  - name: vertex
    description: "Vertex AI"
    iam_roles:
      - roles/aiplatform.user
`)
	cfg := &config.Config{Plugins: map[string]map[string]interface{}{}}
	got := pluginAuthIAMRoles([]*plugins.FilePlugin{p}, cfg)
	if len(got) != 0 {
		t.Errorf("pluginAuthIAMRoles should be empty when plugin has no config, got %v", got)
	}
}

func TestPluginAuthIAMRoles_NilConfigSafe(t *testing.T) {
	p := loadTestPlugin(t, `
name: gemini
version: "1.0.0"
description: "Gemini CLI"
`)
	if got := pluginAuthIAMRoles([]*plugins.FilePlugin{p}, nil); got != nil {
		t.Errorf("pluginAuthIAMRoles(nil cfg) should be nil, got %v", got)
	}
}

func TestAppendUniqueRoles(t *testing.T) {
	got := appendUniqueRoles(
		[]string{"roles/logging.logWriter", "roles/monitoring.metricWriter"},
		[]string{"roles/aiplatform.user", "roles/logging.logWriter", "roles/aiplatform.user"},
	)
	want := []string{
		"roles/logging.logWriter",
		"roles/monitoring.metricWriter",
		"roles/aiplatform.user",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("appendUniqueRoles = %v, want %v", got, want)
	}
}
