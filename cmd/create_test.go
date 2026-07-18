// ra/cmd/create_test.go
package cmd

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
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
name: example
version: "1.0.0"
description: "Example plugin"
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
			"example": {
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
name: example
version: "1.0.0"
description: "Example plugin"
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
			"example": {
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
name: example
version: "1.0.0"
description: "Example plugin"
auth_providers:
  - name: vertex
    description: "Vertex AI"
    iam_roles:
      - roles/aiplatform.user
`)
	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"example": {
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
name: example
version: "1.0.0"
description: "Example plugin"
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
name: example
version: "1.0.0"
description: "Example plugin"
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

// withRegistry temporarily replaces the package-level plugin registry
// with one containing only the plugins yamlSrc declares (each entry is
// a full plugin.yaml string). Restores the previous registry on test
// completion. Tests must NOT call t.Parallel() while using this.
func withRegistry(t *testing.T, yamlSrcs ...string) {
	t.Helper()
	prev := reg
	loaded := make([]*plugins.FilePlugin, 0, len(yamlSrcs))
	for _, src := range yamlSrcs {
		loaded = append(loaded, loadTestPlugin(t, src))
	}
	reg = plugins.NewRegistry(loaded)
	t.Cleanup(func() { reg = prev })
}

const testBucketPluginYAML = `
name: test-bucket-plugin
version: "1.0.0"
description: "fixture for plugin-buckets tests"
buckets:
  - name: state
    name_template: "ra-test-{project_id}-{user_email_hash}"
    override_field: bucket_name_override
    role: roles/storage.objectAdmin
    env_var: RA_PLUGIN_TEST_BUCKET_PLUGIN_BUCKET
config:
  - name: enabled
    type: bool
    default: false
  - name: bucket_name_override
    type: string
`

func TestPluginBuckets_NoEnabledPluginsReturnsNil(t *testing.T) {
	withRegistry(t, testBucketPluginYAML)
	cfg := &config.Config{
		Gcloud: config.GcloudConfig{Project: "p"},
		Git:    config.GitConfig{UserEmail: "a@x.com"},
		Plugins: map[string]map[string]interface{}{
			"test-bucket-plugin": {"enabled": false},
		},
	}
	got, err := pluginBuckets(reg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no buckets, got %v", got)
	}
}

func TestPluginBuckets_EnabledPluginEnumerated(t *testing.T) {
	withRegistry(t, testBucketPluginYAML)
	cfg := &config.Config{
		Gcloud: config.GcloudConfig{Project: "my-proj"},
		Git:    config.GitConfig{UserEmail: "adam@example.com"},
		Plugins: map[string]map[string]interface{}{
			"test-bucket-plugin": {"enabled": true},
		},
	}
	got, err := pluginBuckets(reg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bucket, got %d: %v", len(got), got)
	}
	b := got[0]
	if b.Plugin != "test-bucket-plugin" {
		t.Errorf("plugin name = %q, want %q", b.Plugin, "test-bucket-plugin")
	}
	if !strings.HasPrefix(b.Name, "ra-test-my-proj-") {
		t.Errorf("resolved name has wrong prefix: %q", b.Name)
	}
	if b.Spec.Role != "roles/storage.objectAdmin" {
		t.Errorf("role lost in resolution: %q", b.Spec.Role)
	}
	if b.Spec.EnvVar != "RA_PLUGIN_TEST_BUCKET_PLUGIN_BUCKET" {
		t.Errorf("env_var lost in resolution: %q", b.Spec.EnvVar)
	}
}

func TestPluginBuckets_OverrideFieldHonored(t *testing.T) {
	withRegistry(t, testBucketPluginYAML)
	cfg := &config.Config{
		Gcloud: config.GcloudConfig{Project: "p"},
		Git:    config.GitConfig{UserEmail: "a@x.com"},
		Plugins: map[string]map[string]interface{}{
			"test-bucket-plugin": {
				"enabled":              true,
				"bucket_name_override": "my-explicit-bucket",
			},
		},
	}
	got, err := pluginBuckets(reg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "my-explicit-bucket" {
		t.Errorf("override not honored: %v", got)
	}
}

func TestPluginBuckets_RejectsInvalidOverride(t *testing.T) {
	withRegistry(t, testBucketPluginYAML)
	cfg := &config.Config{
		Gcloud: config.GcloudConfig{Project: "p"},
		Git:    config.GitConfig{UserEmail: "a@x.com"},
		Plugins: map[string]map[string]interface{}{
			"test-bucket-plugin": {
				"enabled":              true,
				"bucket_name_override": "Invalid_NAME",
			},
		},
	}
	_, err := pluginBuckets(reg, cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid bucket name override")
	}
}

func TestBuildContainerEnv_InjectsPluginBucketsAndPropagates(t *testing.T) {
	withRegistry(t, testBucketPluginYAML)
	sleep := 2
	cfg := &config.Config{
		Git: config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"},
		Gcloud: config.GcloudConfig{Project: "p", Workstation: config.WorkstationConfig{
			Region: "us-central1", MachineType: "e2-standard-4", DiskSizeGB: 50,
			ClusterName: "c", ConfigName: "cf", ServiceAccountName: "sa",
			ImageName: "i", ImageTag: "t", RepoName: "r",
			AutoSleepHours: &sleep, AutoShutdownHours: 12,
		}},
		Plugins: map[string]map[string]interface{}{
			"test-bucket-plugin": {"enabled": true},
		},
	}
	got := buildContainerEnv(cfg)
	if !strings.Contains(got, "RA_PLUGIN_TEST_BUCKET_PLUGIN_BUCKET=ra-test-p-") {
		t.Errorf("expected plugin bucket env in container env; got %q", got)
	}
	// Must also be in RA_PROPAGATE_KEYS so 200_remote-agent-setup.sh
	// re-exports it to /run/ra/env for the workstation-side scripts.
	parts := strings.Split(got, ",")
	var propagate string
	for _, p := range parts {
		if strings.HasPrefix(p, "RA_PROPAGATE_KEYS=") {
			propagate = strings.TrimPrefix(p, "RA_PROPAGATE_KEYS=")
		}
	}
	if !strings.Contains(propagate, "RA_PLUGIN_TEST_BUCKET_PLUGIN_BUCKET") {
		t.Errorf("plugin bucket env missing from RA_PROPAGATE_KEYS=%q", propagate)
	}
}

func TestBuildContainerEnv_OmitsPluginBucketsWhenDisabled(t *testing.T) {
	withRegistry(t, testBucketPluginYAML)
	cfg := &config.Config{
		Git:    config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"},
		Gcloud: config.GcloudConfig{Project: "p"},
		Plugins: map[string]map[string]interface{}{
			"test-bucket-plugin": {"enabled": false},
		},
	}
	got := buildContainerEnv(cfg)
	// Look for the EXACT bucket env var key, not a substring — the
	// plugin's `bucket_name_override` config field also serializes to
	// RA_PLUGIN_TEST_BUCKET_PLUGIN_BUCKET_NAME_OVERRIDE which would
	// otherwise false-positive a substring check.
	for _, p := range strings.Split(got, ",") {
		k, _, _ := strings.Cut(p, "=")
		if k == "RA_PLUGIN_TEST_BUCKET_PLUGIN_BUCKET" {
			t.Errorf("expected no plugin bucket env when disabled; key present in %q", got)
		}
	}
}

// envMap parses a buildContainerEnv result into a key→value map for the
// stable-id tests below.
func envMap(s string) map[string]string {
	m := map[string]string{}
	for _, p := range strings.Split(s, ",") {
		if kv := strings.SplitN(p, "=", 2); len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return m
}

func TestStableWorkstationID_DeterministicAndStableAcrossRebuilds(t *testing.T) {
	a := stableWorkstationID("my-proj", "ra-default", "ra-my-proj-adam", "Adam@Example.com")
	b := stableWorkstationID("my-proj", "ra-default", "ra-my-proj-adam", "Adam@Example.com")
	if a != b {
		t.Fatalf("stableWorkstationID not deterministic: %q vs %q", a, b)
	}
	// Case/whitespace in inputs must not change the result — a rebuild
	// reading the same config (possibly re-cased) must reuse the id.
	c := stableWorkstationID("  MY-PROJ ", "RA-DEFAULT", " RA-MY-PROJ-ADAM", "adam@example.com")
	if a != c {
		t.Errorf("stableWorkstationID not normalization-stable: %q vs %q", a, c)
	}
	// GCS/path safety: lowercase, single segment, allowed charset only.
	if a != strings.ToLower(a) {
		t.Errorf("id not lowercase: %q", a)
	}
	if strings.ContainsAny(a, "/ .") {
		t.Errorf("id contains unsafe char: %q", a)
	}
	if !regexp.MustCompile(`^[a-z0-9_-]+$`).MatchString(a) {
		t.Errorf("id has out-of-charset chars: %q", a)
	}
}

func TestStableWorkstationID_DistinctInputsDistinctIDs(t *testing.T) {
	cases := [][4]string{
		{"proj-a", "cfg", "ws-1", "u@x.com"},
		{"proj-b", "cfg", "ws-1", "u@x.com"},
		{"proj-a", "cfg2", "ws-1", "u@x.com"},
		{"proj-a", "cfg", "ws-1", "v@x.com"},
		// Two workstations on one config must not share an id.
		{"proj-a", "cfg", "ws-2", "u@x.com"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		id := stableWorkstationID(c[0], c[1], c[2], c[3])
		if seen[id] {
			t.Errorf("collision for %v -> %q", c, id)
		}
		seen[id] = true
	}
}

func TestStableWorkstationID_EmptyInputsYieldEmpty(t *testing.T) {
	if id := stableWorkstationID("", "", "", ""); id != "" {
		t.Errorf("expected empty id for empty inputs, got %q", id)
	}
}

// testStableIDPluginYAML is a fixture plugin that opts into stable-id
// injection via the generic `stable_id:` declaration — exercising the
// mechanism without naming any production plugin.
const testStableIDPluginYAML = `
name: test-stable-plugin
version: "1.0.0"
description: "fixture for stable_id injection tests"
stable_id:
  env_var: RA_PLUGIN_TEST_STABLE_PLUGIN_ID
config:
  - name: enabled
    type: bool
    default: false
`

func TestBuildContainerEnv_InjectsStableIDWhenPluginEnabled(t *testing.T) {
	withRegistry(t, testStableIDPluginYAML)
	cfg := &config.Config{
		Git: config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"},
		Gcloud: config.GcloudConfig{Project: "p", Workstation: config.WorkstationConfig{
			ConfigName: "ra-default",
		}},
		WorkstationName: "ra-p-alice",
		Plugins: map[string]map[string]interface{}{
			"test-stable-plugin": {"enabled": true},
		},
	}
	m := envMap(buildContainerEnv(cfg))
	want := stableWorkstationID("p", "ra-default", "ra-p-alice", "a@x.com")
	// The env-var name comes from the plugin's own `stable_id.env_var`
	// declaration — core hard-codes nothing.
	if m["RA_PLUGIN_TEST_STABLE_PLUGIN_ID"] != want {
		t.Errorf("RA_PLUGIN_TEST_STABLE_PLUGIN_ID = %q, want %q", m["RA_PLUGIN_TEST_STABLE_PLUGIN_ID"], want)
	}
	// Must be propagated so 200_remote-agent-setup.sh re-exports it to
	// /run/ra/env where the plugin's startup scripts read it.
	if !strings.Contains(m["RA_PROPAGATE_KEYS"], "RA_PLUGIN_TEST_STABLE_PLUGIN_ID") {
		t.Errorf("stable id missing from RA_PROPAGATE_KEYS=%q", m["RA_PROPAGATE_KEYS"])
	}
}

func TestBuildContainerEnv_OmitsStableIDWhenPluginDisabled(t *testing.T) {
	withRegistry(t, testStableIDPluginYAML)
	cfg := &config.Config{
		Git:    config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"},
		Gcloud: config.GcloudConfig{Project: "p"},
		Plugins: map[string]map[string]interface{}{
			"test-stable-plugin": {"enabled": false},
		},
	}
	m := envMap(buildContainerEnv(cfg))
	if _, ok := m["RA_PLUGIN_TEST_STABLE_PLUGIN_ID"]; ok {
		t.Errorf("stable id must not be injected when plugin disabled")
	}
}

func TestUniqueBucketsWithLifecycle_UnionsSharedBucketRulesInOrder(t *testing.T) {
	coldline := plugins.BucketSpec{Lifecycle: []plugins.LifecycleRule{
		{Action: "SetStorageClass", StorageClass: "COLDLINE", AgeDays: 90, Prefixes: []string{"a/"}},
	}}
	del := plugins.BucketSpec{Lifecycle: []plugins.LifecycleRule{
		{Action: "Delete", AgeDays: 365, Prefixes: []string{"b/"}},
	}}
	buckets := []resolvedBucket{
		{Plugin: "p1", Name: "shared", Spec: coldline},
		{Plugin: "p2", Name: "solo", Spec: del},
		{Plugin: "p3", Name: "shared", Spec: del}, // same bucket as p1
	}
	order, lifecycle := uniqueBucketsWithLifecycle(buckets)

	// Unique names, first-seen order — "shared" before "solo", listed once.
	if got := strings.Join(order, ","); got != "shared,solo" {
		t.Fatalf("order = %q, want %q", got, "shared,solo")
	}
	// Rules for the shared bucket must be the UNION of p1 and p3 (not
	// clobbered), in plugin order.
	if rs := lifecycle["shared"]; len(rs) != 2 ||
		rs[0].Action != "SetStorageClass" || rs[1].Action != "Delete" {
		t.Errorf("shared lifecycle not unioned in order: %+v", rs)
	}
	if rs := lifecycle["solo"]; len(rs) != 1 || rs[0].Action != "Delete" {
		t.Errorf("solo lifecycle wrong: %+v", rs)
	}
}

func TestUniqueBucketsWithLifecycle_NoLifecycleStillListsBucket(t *testing.T) {
	buckets := []resolvedBucket{{Plugin: "p", Name: "b", Spec: plugins.BucketSpec{}}}
	order, lifecycle := uniqueBucketsWithLifecycle(buckets)
	if len(order) != 1 || order[0] != "b" {
		t.Fatalf("order = %v, want [b]", order)
	}
	if len(lifecycle["b"]) != 0 {
		t.Errorf("expected no lifecycle rules for b, got %+v", lifecycle["b"])
	}
}

func TestLifecycleRulesFor_ConvertsSpec(t *testing.T) {
	spec := plugins.BucketSpec{
		Lifecycle: []plugins.LifecycleRule{
			{Action: "SetStorageClass", StorageClass: "COLDLINE", AgeDays: 90, Prefixes: []string{"plugin-data/"}},
			{Action: "Delete", AgeDays: 365},
		},
	}
	got := lifecycleRulesFor(spec)
	if len(got) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(got))
	}
	if got[0].Action != "SetStorageClass" || got[0].StorageClass != "COLDLINE" || got[0].AgeDays != 90 ||
		len(got[0].Prefixes) != 1 || got[0].Prefixes[0] != "plugin-data/" {
		t.Errorf("rule 0 not converted faithfully: %+v", got[0])
	}
	if got[1].Action != "Delete" || got[1].AgeDays != 365 {
		t.Errorf("rule 1 not converted faithfully: %+v", got[1])
	}
	if lifecycleRulesFor(plugins.BucketSpec{}) != nil {
		t.Errorf("expected nil for spec with no lifecycle rules")
	}
}
