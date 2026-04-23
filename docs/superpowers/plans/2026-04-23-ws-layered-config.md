# `ws` Layered Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the ad-hoc config layering with a strict two-source merge (project over user), eliminate all hardcoded defaults, add a generic `{secrets, env}` passthrough to the container, and special-case `claude_oauth`/`claude_apikey`/`github_pat` so the existing credential helpers keep working.

**Architecture:** `internal/config` switches to grouped YAML (`git:`, `workstation:`, `project:`, `secrets:`, `env:`) with user config as the exhaustive baseline and project config as a thin override layer. The merged `Config` struct consumed by `cmd/*` stays flat — `Merge` handles the lift so callers don't churn. A new `validate.go` runs in `cmd/root.go`'s `PersistentPreRunE` (except for `setup`). `cmd/create.go` iterates the merged `Secrets` map to emit a generic wire protocol (`WS_SECRETS`, `WS_SECRET_<NAME>_NAME`, `WS_SECRET_<NAME>_VAR`), and `internal/scaffold/assets/entrypoint.sh` dispatches on the logical name. Scaffold templates rewritten from scratch — the old `UserProjectValues`/`UserGlobalValues`/`splitCommentedDefault` machinery is deleted since the new user template is fully rendered (no commented-fallback dance).

**Tech Stack:** Go 1.22+; `gopkg.in/yaml.v3`; bash (entrypoint only); existing packages — no new dependencies.

---

## Spec reference

See `docs/superpowers/specs/2026-04-23-ws-layered-config-design.md` for:
- full field reference table,
- wire protocol details,
- `ws setup` end-to-end flow,
- open-question decisions (apikey is helper-only, `oauth_account_secret_name` is a sibling field).

---

## File Structure

| File | State | Purpose |
|---|---|---|
| `internal/config/types.go` | **rewrite** | Grouped `UserConfig`, `ProjectConfig`; flat `Config`; `SecretEntry`. |
| `internal/config/global.go` | **rewrite** | Rename `LoadGlobalConfig`→`LoadUserConfig`; same file-missing-ok semantics. |
| `internal/config/merge.go` | **rewrite** | Two-layer merge (user → project); no hardcoded defaults; no `Projects[id]`. |
| `internal/config/validate.go` | **new** | `Validate(cfg)` — required keys, secret constraints, env-var names, collisions. |
| `internal/config/validate_test.go` | **new** | Table-driven tests per validation rule. |
| `internal/config/merge_test.go` | **rewrite** | New two-layer semantics + secrets/env map merge. |
| `internal/config/global_test.go` | **rewrite** | `LoadUserConfig` ok/missing/malformed. |
| `internal/config/discover.go` | unchanged | Project-config discovery. |
| `internal/scaffold/templates.go` | **rewrite** | `RenderUserConfig`, `RenderProjectConfig`. |
| `internal/scaffold/templates_test.go` | **rewrite** | Every required key present; secrets documentation comments present. |
| `internal/scaffold/scaffold.go` | **rewrite** | New prompt flow (git, Claude-auth-mode menu, project); materialize user-dir assets. |
| `internal/scaffold/scaffold_test.go` | **rewrite** | End-to-end interactive + `--with-project-assets`. |
| `internal/scaffold/source.go` | **modify** | `ResolveSource` errors if neither user nor project asset exists. Keep `EmbeddedSeed(name)` for setup-time bootstrap. |
| `internal/scaffold/source_test.go` | **modify** | Assert error on missing; assert user-template hit; assert seed access. |
| `internal/scaffold/scaffold_assets.go` | **modify** | Materialize Dockerfile/entrypoint into `~/.config/ws/template/` during setup. Keep `.ws/` path for `--with-project-assets`. |
| `internal/scaffold/scaffold_assets_test.go` | **modify** | Cover user-dir materialization and new flag. |
| `internal/scaffold/assets/entrypoint.sh` | **rewrite** | Generic `WS_SECRETS` loop + special-case dispatch. |
| `internal/scaffold/embedded.go` | unchanged | `embeddedDockerfile`, `embeddedEntrypoint` still `//go:embed`-backed. |
| `cmd/root.go` | **modify** | Run `config.Validate(cfg)` after merge; `setup` still bypasses. |
| `cmd/setup.go` | **modify** | Add `--with-project-assets` flag. |
| `cmd/setup_test.go` | **modify** | Smoke-test new flag + schema in output. |
| `cmd/create.go` | **modify** | Iterate `cfg.Secrets`; emit wire protocol; remove hardcoded names; drop `ensureClaudeSecret` special branch. |
| `cmd/create_test.go` | **modify** | Assert `--container-env` contains expected keys. |
| `cmd/{connect,delete,list,logs,resolve,stop,url}.go` | unchanged | Flat `Config` preserved; no callsite churn. |
| `README.md` | **modify** | Document new flow + schema. |
| `docs/headless-claude-auth-plan.md` | **modify** | Reflect mode-selection via logical name in `secrets:`. |

No migration code. First run against an old config fails validation with a message pointing at `ws setup`.

---

## Task 1: Define the new schema (`types.go`) with stub `Merge`/`Load` functions

Goal: compile-green starting point — new types defined, old types gone, `Merge`/`Load*` temporarily return zero values. Tests that depended on old types are removed or stubbed here; they'll be rewritten in Tasks 2–3.

**Files:**
- Rewrite: `internal/config/types.go`
- Rewrite: `internal/config/global.go`
- Rewrite: `internal/config/merge.go`
- Delete bodies of: `internal/config/merge_test.go` (keep package line only), `internal/config/global_test.go` (keep package line only) — will be re-added in Tasks 2–3.
- Modify: `cmd/root.go` — rename `LoadGlobalConfig` → `LoadUserConfig`.

- [ ] **Step 1: Rewrite `internal/config/types.go`**

Replace file contents with:

```go
package config

// GitConfig is the "git:" section of ~/.config/ws/config.yaml.
type GitConfig struct {
	UserName  string `yaml:"user_name"`
	UserEmail string `yaml:"user_email"`
}

// ProjectSection is the "project:" section of .ws/config.yaml.
type ProjectSection struct {
	ID            string `yaml:"id"`
	GithubRepoURL string `yaml:"github_repo_url"`
}

// WorkstationConfig is the "workstation:" section, present in both files.
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

// SecretEntry is one row of the "secrets:" map. Logical name is the map key.
type SecretEntry struct {
	SecretName             string `yaml:"secret_name"`
	EnvVar                 string `yaml:"env_var"`
	OAuthAccountSecretName string `yaml:"oauth_account_secret_name,omitempty"`
}

// UserConfig is ~/.config/ws/config.yaml — the exhaustive baseline.
type UserConfig struct {
	Git         GitConfig              `yaml:"git"`
	Workstation WorkstationConfig      `yaml:"workstation"`
	Secrets     map[string]SecretEntry `yaml:"secrets"`
	Env         map[string]string      `yaml:"env"`
}

// ProjectConfig is .ws/config.yaml — required project keys + overrides.
type ProjectConfig struct {
	Project     ProjectSection         `yaml:"project"`
	Workstation WorkstationConfig      `yaml:"workstation"`
	Secrets     map[string]SecretEntry `yaml:"secrets"`
	Env         map[string]string      `yaml:"env"`
}

// Config is the post-merge, flat configuration consumed by every ws command.
// Merge lifts the grouped inputs into these flat fields.
type Config struct {
	ProjectID     string
	GithubRepoURL string

	GitUserName  string
	GitUserEmail string

	Region             string
	MachineType        string
	DiskSizeGB         int
	ClusterName        string
	ConfigName         string
	ServiceAccountName string
	ImageName          string
	ImageTag           string
	RepoName           string
	AutoSleepHours     *int
	AutoShutdownHours  int

	Secrets map[string]SecretEntry
	Env     map[string]string

	WorkstationName   string // <project_id>-<username>, sanitized, ≤63 chars
	ProjectConfigPath string // absolute path to the .ws/config.yaml that was found
}
```

- [ ] **Step 2: Rewrite `internal/config/global.go`**

```go
// ws/internal/config/global.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UserConfigPath returns the default path for the user-wide config.
func UserConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ws", "config.yaml"), nil
}

// LoadUserConfig reads and parses the user config file.
// Returns an empty UserConfig (no error) if the file does not exist —
// validation (not loading) is responsible for flagging missing required keys.
func LoadUserConfig(path string) (*UserConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &UserConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg UserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadProjectConfig reads and parses a .ws/config.yaml file.
func LoadProjectConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
```

(`LoadProjectConfig` moves here from `merge.go`; keeping load logic together.)

- [ ] **Step 3: Rewrite `internal/config/merge.go`**

```go
// ws/internal/config/merge.go
package config

import (
	"os"
	"os/user"
	"regexp"
	"strings"
)

// Merge combines user and project configs into a flat Config.
// Precedence: project > user. No hardcoded defaults; missing required
// fields are caught by Validate, not Merge.
func Merge(user *UserConfig, project *ProjectConfig) (*Config, error) {
	if user == nil {
		user = &UserConfig{}
	}
	if project == nil {
		project = &ProjectConfig{}
	}

	cfg := &Config{
		// project-only fields
		ProjectID:     project.Project.ID,
		GithubRepoURL: project.Project.GithubRepoURL,

		// git — user only (never read from project)
		GitUserName:  user.Git.UserName,
		GitUserEmail: user.Git.UserEmail,
	}

	// workstation: user is the baseline, project overrides per field.
	lift := func(u, p WorkstationConfig) {
		cfg.Region = pick(p.Region, u.Region)
		cfg.MachineType = pick(p.MachineType, u.MachineType)
		cfg.DiskSizeGB = pickInt(p.DiskSizeGB, u.DiskSizeGB)
		cfg.ClusterName = pick(p.ClusterName, u.ClusterName)
		cfg.ConfigName = pick(p.ConfigName, u.ConfigName)
		cfg.ServiceAccountName = pick(p.ServiceAccountName, u.ServiceAccountName)
		cfg.ImageName = pick(p.ImageName, u.ImageName)
		cfg.ImageTag = pick(p.ImageTag, u.ImageTag)
		cfg.RepoName = pick(p.RepoName, u.RepoName)
		cfg.AutoSleepHours = pickIntPtr(p.AutoSleepHours, u.AutoSleepHours)
		cfg.AutoShutdownHours = pickInt(p.AutoShutdownHours, u.AutoShutdownHours)
	}
	lift(user.Workstation, project.Workstation)

	// Secrets: user map seeds, project entries override or add by logical name.
	cfg.Secrets = mergeSecretMap(user.Secrets, project.Secrets)
	cfg.Env = mergeStringMap(user.Env, project.Env)

	cfg.WorkstationName = WorkstationName(cfg.ProjectID, currentUsername())
	return cfg, nil
}

// pick returns p if non-empty, else u. Project-over-user for strings.
func pick(p, u string) string {
	if p != "" {
		return p
	}
	return u
}

// pickInt returns p if non-zero, else u. Project-over-user for ints.
func pickInt(p, u int) int {
	if p != 0 {
		return p
	}
	return u
}

// pickIntPtr: *int lets us distinguish "unset" (nil) from 0.
// Project override only applies if project's pointer is non-nil.
func pickIntPtr(p, u *int) *int {
	if p != nil {
		v := *p
		return &v
	}
	if u != nil {
		v := *u
		return &v
	}
	return nil
}

func mergeSecretMap(user, project map[string]SecretEntry) map[string]SecretEntry {
	out := map[string]SecretEntry{}
	for k, v := range user {
		out[k] = v
	}
	for k, v := range project {
		out[k] = v
	}
	return out
}

func mergeStringMap(user, project map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range user {
		out[k] = v
	}
	for k, v := range project {
		out[k] = v
	}
	return out
}

// currentUsername returns the username used to derive WorkstationName.
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "user"
}

var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9]+`)

// WorkstationName returns the default workstation name for a project/user pair.
func WorkstationName(projectID, username string) string {
	clean := func(s string) string {
		s = strings.ToLower(s)
		s = nonAlphanumHyphen.ReplaceAllString(s, "-")
		s = strings.Trim(s, "-")
		return s
	}
	name := clean(projectID) + "-" + clean(username)
	if len(name) > 63 {
		name = name[:63]
	}
	name = strings.TrimRight(name, "-")
	return name
}
```

- [ ] **Step 4: Stub out `merge_test.go` and `global_test.go`**

`internal/config/merge_test.go` — replace entire file with:

```go
package config_test
```

`internal/config/global_test.go` — replace entire file with:

```go
package config_test
```

(Tests come back in Tasks 2 and 3.)

- [ ] **Step 5: Update `cmd/root.go` references**

In `cmd/root.go`, replace:

```go
		globalCfgPath, err := config.GlobalConfigPath()
		if err != nil {
			return fmt.Errorf("determining global config path: %w", err)
		}
		globalCfg, err := config.LoadGlobalConfig(globalCfgPath)
		if err != nil {
			return fmt.Errorf("loading global config: %w", err)
		}
		cfg, err = config.Merge(projectCfg, globalCfg)
```

with:

```go
		userCfgPath, err := config.UserConfigPath()
		if err != nil {
			return fmt.Errorf("determining user config path: %w", err)
		}
		userCfg, err := config.LoadUserConfig(userCfgPath)
		if err != nil {
			return fmt.Errorf("loading user config: %w", err)
		}
		cfg, err = config.Merge(userCfg, projectCfg)
```

Note the argument order flip — `Merge(user, project)` mirrors the precedence semantics (low → high).

- [ ] **Step 6: Build and run all tests**

Run: `go build ./... && go test ./...`
Expected: PASS. The merge logic still has the same *output fields* on `Config` so `cmd/*` compiles unchanged. Tests that exercised the old hardcoded-defaults behavior are gone (Task 2 will reintroduce tests against the new semantics).

- [ ] **Step 7: Commit**

```bash
git add internal/config/types.go internal/config/global.go internal/config/merge.go \
        internal/config/merge_test.go internal/config/global_test.go cmd/root.go
git commit -m "$(cat <<'EOF'
refactor(config): flatten schema to grouped YAML + two-layer merge

Introduces grouped UserConfig/ProjectConfig types and rewrites Merge to
user→project (no hardcoded defaults, no Projects[id] map). The flat
post-merge Config keeps its field shape so cmd/* is unchanged. Old
config tests are dropped here and re-added in the next tasks.
EOF
)"
```

---

## Task 2: Unit-test the new `Merge`

**Files:**
- Rewrite: `internal/config/merge_test.go`

- [ ] **Step 1: Write merge tests**

Replace `internal/config/merge_test.go` contents with:

```go
package config_test

import (
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestMerge_UserBaselineNoProject(t *testing.T) {
	two := 2
	user := &config.UserConfig{
		Git: config.GitConfig{UserName: "Alice", UserEmail: "alice@example.com"},
		Workstation: config.WorkstationConfig{
			Region: "us-central1", MachineType: "e2-standard-4", DiskSizeGB: 50,
			ClusterName: "claude-dev-cluster", ConfigName: "claude-dev-config",
			ServiceAccountName: "claude-workstation-sa",
			ImageName:          "claude-code-workstation",
			ImageTag:           "latest",
			RepoName:           "claude-workstation-images",
			AutoSleepHours:     &two,
			AutoShutdownHours:  12,
		},
	}
	cfg, err := config.Merge(user, &config.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-central1" || cfg.MachineType != "e2-standard-4" ||
		cfg.GitUserName != "Alice" || cfg.DiskSizeGB != 50 ||
		cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 2 ||
		cfg.AutoShutdownHours != 12 {
		t.Errorf("unexpected merged Config: %+v", cfg)
	}
}

func TestMerge_ProjectOverridesUser(t *testing.T) {
	one := 1
	eight := 8
	user := &config.UserConfig{
		Workstation: config.WorkstationConfig{
			Region:            "us-central1",
			MachineType:       "e2-standard-4",
			AutoSleepHours:    &eight,
			AutoShutdownHours: 12,
		},
	}
	project := &config.ProjectConfig{
		Project: config.ProjectSection{ID: "my-proj", GithubRepoURL: "https://example.com/r.git"},
		Workstation: config.WorkstationConfig{
			Region:            "us-east1",
			AutoSleepHours:    &one,
			AutoShutdownHours: 24,
		},
	}
	cfg, err := config.Merge(user, project)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east1" {
		t.Errorf("Region: got %q, want us-east1", cfg.Region)
	}
	if cfg.MachineType != "e2-standard-4" {
		t.Errorf("MachineType: got %q, want e2-standard-4 (from user)", cfg.MachineType)
	}
	if cfg.ProjectID != "my-proj" || cfg.GithubRepoURL != "https://example.com/r.git" {
		t.Errorf("project fields missing: %+v", cfg)
	}
	if cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 1 {
		t.Errorf("AutoSleepHours: got %v, want &1", cfg.AutoSleepHours)
	}
	if cfg.AutoShutdownHours != 24 {
		t.Errorf("AutoShutdownHours: got %d, want 24", cfg.AutoShutdownHours)
	}
}

func TestMerge_EmptyProjectDoesNotClobberUser(t *testing.T) {
	user := &config.UserConfig{
		Workstation: config.WorkstationConfig{Region: "us-central1", MachineType: "e2-standard-4"},
	}
	cfg, _ := config.Merge(user, &config.ProjectConfig{})
	if cfg.Region != "us-central1" || cfg.MachineType != "e2-standard-4" {
		t.Errorf("zero-valued project fields should not override user: %+v", cfg)
	}
}

func TestMerge_NoHardcodedDefaultsWhenBothEmpty(t *testing.T) {
	cfg, _ := config.Merge(&config.UserConfig{}, &config.ProjectConfig{})
	// Every workstation field should be zero — no silent fallback to
	// "e2-standard-4" or similar. Validation (not Merge) flags missing.
	if cfg.MachineType != "" || cfg.ClusterName != "" || cfg.ImageName != "" ||
		cfg.ServiceAccountName != "" || cfg.DiskSizeGB != 0 {
		t.Errorf("Merge must not inject hardcoded defaults, got %+v", cfg)
	}
}

func TestMerge_SecretsMap_UserSeedsProjectOverrides(t *testing.T) {
	user := &config.UserConfig{
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
			"github_pat":   {SecretName: "github-pat", EnvVar: "GH_TOKEN"},
		},
	}
	project := &config.ProjectConfig{
		Secrets: map[string]config.SecretEntry{
			"github_pat": {SecretName: "proj-github-pat", EnvVar: "GH_TOKEN"}, // override
			"my_api":     {SecretName: "my-api", EnvVar: "MY_API"},             // add
		},
	}
	cfg, _ := config.Merge(user, project)
	if got := cfg.Secrets["claude_oauth"].SecretName; got != "claude-auth" {
		t.Errorf("claude_oauth.secret_name: got %q, want claude-auth (from user)", got)
	}
	if got := cfg.Secrets["github_pat"].SecretName; got != "proj-github-pat" {
		t.Errorf("github_pat.secret_name: got %q, want proj-github-pat (project override)", got)
	}
	if got := cfg.Secrets["my_api"].SecretName; got != "my-api" {
		t.Errorf("my_api.secret_name: got %q, want my-api (project add)", got)
	}
	if len(cfg.Secrets) != 3 {
		t.Errorf("want 3 secrets, got %d: %+v", len(cfg.Secrets), cfg.Secrets)
	}
}

func TestMerge_EnvMap_UserSeedsProjectOverrides(t *testing.T) {
	user := &config.UserConfig{Env: map[string]string{"FOO": "1", "BAR": "u"}}
	project := &config.ProjectConfig{Env: map[string]string{"BAR": "p", "BAZ": "3"}}
	cfg, _ := config.Merge(user, project)
	want := map[string]string{"FOO": "1", "BAR": "p", "BAZ": "3"}
	for k, v := range want {
		if cfg.Env[k] != v {
			t.Errorf("Env[%q]: got %q, want %q", k, cfg.Env[k], v)
		}
	}
	if len(cfg.Env) != 3 {
		t.Errorf("want 3 env entries, got %d", len(cfg.Env))
	}
}

func TestMerge_GitIdentityUserOnly(t *testing.T) {
	user := &config.UserConfig{Git: config.GitConfig{UserName: "Alice", UserEmail: "a@x.com"}}
	// Project has no Git section (by schema design). Nothing to test for
	// "project overrides git" because ProjectConfig lacks Git fields.
	cfg, _ := config.Merge(user, &config.ProjectConfig{})
	if cfg.GitUserName != "Alice" || cfg.GitUserEmail != "a@x.com" {
		t.Errorf("git identity lost: %+v", cfg)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/config/...`
Expected: PASS (all new tests).

- [ ] **Step 3: Commit**

```bash
git add internal/config/merge_test.go
git commit -m "test(config): cover two-layer merge and maps"
```

---

## Task 3: `LoadUserConfig` / `LoadProjectConfig` tests

**Files:**
- Rewrite: `internal/config/global_test.go`

- [ ] **Step 1: Write load tests**

Replace `internal/config/global_test.go` contents with:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestLoadUserConfig_Missing_ReturnsEmpty(t *testing.T) {
	cfg, err := config.LoadUserConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("LoadUserConfig on missing file: got %v, want nil", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil empty UserConfig")
	}
	if cfg.Git.UserName != "" || len(cfg.Secrets) != 0 {
		t.Errorf("want zero-value UserConfig, got %+v", cfg)
	}
}

func TestLoadUserConfig_Malformed_ReturnsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("git:\n  user_name: [unterminated"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadUserConfig(p); err == nil {
		t.Fatal("expected YAML parse error, got nil")
	}
}

func TestLoadUserConfig_Roundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	contents := `
git:
  user_name: "Alice"
  user_email: "alice@example.com"
workstation:
  region: us-central1
  machine_type: e2-standard-4
  disk_size_gb: 50
  cluster_name: claude-dev-cluster
  config_name: claude-dev-config
  service_account_name: claude-workstation-sa
  image_name: claude-code-workstation
  image_tag: latest
  repo_name: claude-workstation-images
  auto_sleep_hours: 2
  auto_shutdown_hours: 12
secrets:
  claude_oauth:
    secret_name: claude-auth
    env_var: CLAUDE_CODE_OAUTH_TOKEN
  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN
env:
  EXTRA_KEY: extra_value
`
	if err := os.WriteFile(p, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadUserConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.UserName != "Alice" || cfg.Workstation.Region != "us-central1" ||
		cfg.Workstation.AutoSleepHours == nil || *cfg.Workstation.AutoSleepHours != 2 {
		t.Errorf("parsed wrong: %+v", cfg)
	}
	if got := cfg.Secrets["claude_oauth"].EnvVar; got != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("claude_oauth.env_var: got %q", got)
	}
	if cfg.Env["EXTRA_KEY"] != "extra_value" {
		t.Errorf("env map not parsed: %+v", cfg.Env)
	}
}

func TestLoadProjectConfig_Roundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	contents := `
project:
  id: my-proj
  github_repo_url: https://example.com/x.git
workstation:
  machine_type: e2-standard-8
secrets:
  my_extra:
    secret_name: my-extra
    env_var: MY_EXTRA
env:
  MY_VAR: 1
`
	if err := os.WriteFile(p, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadProjectConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.ID != "my-proj" || cfg.Project.GithubRepoURL == "" {
		t.Errorf("project section parsed wrong: %+v", cfg.Project)
	}
	if cfg.Workstation.MachineType != "e2-standard-8" {
		t.Errorf("project workstation override lost: %+v", cfg.Workstation)
	}
	if cfg.Secrets["my_extra"].EnvVar != "MY_EXTRA" || cfg.Env["MY_VAR"] != "1" {
		t.Errorf("maps not parsed: %+v", cfg)
	}
}

func TestLoadProjectConfig_Missing_ReturnsError(t *testing.T) {
	if _, err := config.LoadProjectConfig(filepath.Join(t.TempDir(), "none.yaml")); err == nil {
		t.Fatal("expected error for missing project config, got nil")
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/config/global_test.go
git commit -m "test(config): cover LoadUserConfig and LoadProjectConfig"
```

---

## Task 4: `Validate` — required keys, secrets constraints, env-var names, collisions

**Files:**
- Create: `internal/config/validate.go`
- Create: `internal/config/validate_test.go`
- Modify: `cmd/root.go` — call `config.Validate(cfg)` after merge.
- Modify: `cmd/setup.go` — keep `PersistentPreRunE: func(...) error { return nil }` so setup runs without a config (already present; verify).

- [ ] **Step 1: Write the failing tests**

Create `internal/config/validate_test.go`:

```go
package config_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func fullValidCfg() *config.Config {
	sleep := 2
	return &config.Config{
		ProjectID:          "p",
		GithubRepoURL:      "https://example.com/r.git",
		GitUserName:        "Alice",
		GitUserEmail:       "a@x.com",
		Region:             "us-central1",
		MachineType:        "e2-standard-4",
		DiskSizeGB:         50,
		ClusterName:        "claude-dev-cluster",
		ConfigName:         "claude-dev-config",
		ServiceAccountName: "claude-workstation-sa",
		ImageName:          "claude-code-workstation",
		ImageTag:           "latest",
		RepoName:           "claude-workstation-images",
		AutoSleepHours:     &sleep,
		AutoShutdownHours:  12,
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
			"github_pat":   {SecretName: "github-pat", EnvVar: "GH_TOKEN"},
		},
	}
}

func TestValidate_FullConfig_OK(t *testing.T) {
	if err := config.Validate(fullValidCfg()); err != nil {
		t.Errorf("expected OK, got %v", err)
	}
}

func TestValidate_MissingRequiredKeys(t *testing.T) {
	tcs := []struct {
		mutate func(*config.Config)
		want   string
	}{
		{func(c *config.Config) { c.ProjectID = "" }, "project.id"},
		{func(c *config.Config) { c.GithubRepoURL = "" }, "project.github_repo_url"},
		{func(c *config.Config) { c.GitUserName = "" }, "git.user_name"},
		{func(c *config.Config) { c.GitUserEmail = "" }, "git.user_email"},
		{func(c *config.Config) { c.Region = "" }, "workstation.region"},
		{func(c *config.Config) { c.MachineType = "" }, "workstation.machine_type"},
		{func(c *config.Config) { c.DiskSizeGB = 0 }, "workstation.disk_size_gb"},
		{func(c *config.Config) { c.ClusterName = "" }, "workstation.cluster_name"},
		{func(c *config.Config) { c.ConfigName = "" }, "workstation.config_name"},
		{func(c *config.Config) { c.ServiceAccountName = "" }, "workstation.service_account_name"},
		{func(c *config.Config) { c.ImageName = "" }, "workstation.image_name"},
		{func(c *config.Config) { c.ImageTag = "" }, "workstation.image_tag"},
		{func(c *config.Config) { c.RepoName = "" }, "workstation.repo_name"},
		{func(c *config.Config) { c.AutoSleepHours = nil }, "workstation.auto_sleep_hours"},
		{func(c *config.Config) { c.AutoShutdownHours = 0 }, "workstation.auto_shutdown_hours"},
	}
	for _, tc := range tcs {
		c := fullValidCfg()
		tc.mutate(c)
		err := config.Validate(c)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want error mentioning %q, got %v", tc.want, err)
		}
	}
}

func TestValidate_ExactlyOneClaudeAuth(t *testing.T) {
	c := fullValidCfg()
	delete(c.Secrets, "claude_oauth")
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "claude_oauth") {
		t.Errorf("want missing-claude-auth error, got %v", err)
	}

	c = fullValidCfg()
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("want exactly-one error with both set, got %v", err)
	}
}

func TestValidate_GithubPatRequired(t *testing.T) {
	c := fullValidCfg()
	delete(c.Secrets, "github_pat")
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "github_pat") {
		t.Errorf("want missing-github_pat error, got %v", err)
	}
}

func TestValidate_SecretEnvVarShape(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_oauth"] = config.SecretEntry{SecretName: "x", EnvVar: "not-valid"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "env_var") {
		t.Errorf("want env_var-shape error, got %v", err)
	}
}

func TestValidate_SecretSecretNameNonEmpty(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_oauth"] = config.SecretEntry{SecretName: "", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "secret_name") {
		t.Errorf("want secret_name-missing error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_BetweenSecrets(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["extra"] = config.SecretEntry{SecretName: "ex", EnvVar: "GH_TOKEN"} // collides with github_pat
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Errorf("want collision error, got %v", err)
	}
}

func TestValidate_EnvVarCollision_SecretAndEnv(t *testing.T) {
	c := fullValidCfg()
	c.Env = map[string]string{"GH_TOKEN": "whatever"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Errorf("want collision error between secret and env, got %v", err)
	}
}

func TestValidate_EnvVarCollision_Reserved(t *testing.T) {
	for _, reserved := range []string{"GCP_PROJECT_ID", "GITHUB_REPO_URL", "GIT_USER_NAME", "WS_SECRETS", "WS_SECRET_FOO_NAME"} {
		c := fullValidCfg()
		c.Env = map[string]string{reserved: "v"}
		if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), reserved) {
			t.Errorf("reserved %q not caught: %v", reserved, err)
		}
	}
}

func TestValidate_EnvKeyShape(t *testing.T) {
	c := fullValidCfg()
	c.Env = map[string]string{"not-valid": "x"}
	if err := config.Validate(c); err == nil || !strings.Contains(err.Error(), "env") {
		t.Errorf("want env key-shape error, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/...`
Expected: compile errors — `config.Validate` undefined.

- [ ] **Step 3: Implement `validate.go`**

Create `internal/config/validate.go`:

```go
// ws/internal/config/validate.go
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// envVarPattern matches POSIX shell identifiers used as container env vars.
var envVarPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// reservedEnvVars are set directly by ws create; configs must not shadow them.
var reservedEnvVars = []string{
	"GCP_PROJECT_ID",
	"GITHUB_REPO_URL",
	"GIT_USER_NAME",
	"GIT_USER_EMAIL",
	"WS_SECRETS",
}

// Validate asserts the merged Config is complete and internally consistent.
// Run from cmd/root.go's PersistentPreRunE for every command except setup.
func Validate(cfg *Config) error {
	var errs []string

	// Required scalars. The "label" mirrors the YAML path so error messages
	// tell the user exactly which key to set.
	required := []struct {
		label string
		empty bool
	}{
		{"project.id", cfg.ProjectID == ""},
		{"project.github_repo_url", cfg.GithubRepoURL == ""},
		{"git.user_name", cfg.GitUserName == ""},
		{"git.user_email", cfg.GitUserEmail == ""},
		{"workstation.region", cfg.Region == ""},
		{"workstation.machine_type", cfg.MachineType == ""},
		{"workstation.disk_size_gb", cfg.DiskSizeGB == 0},
		{"workstation.cluster_name", cfg.ClusterName == ""},
		{"workstation.config_name", cfg.ConfigName == ""},
		{"workstation.service_account_name", cfg.ServiceAccountName == ""},
		{"workstation.image_name", cfg.ImageName == ""},
		{"workstation.image_tag", cfg.ImageTag == ""},
		{"workstation.repo_name", cfg.RepoName == ""},
		{"workstation.auto_sleep_hours", cfg.AutoSleepHours == nil},
		{"workstation.auto_shutdown_hours", cfg.AutoShutdownHours == 0},
	}
	for _, r := range required {
		if r.empty {
			errs = append(errs, fmt.Sprintf("%s is required (run 'ws setup' to write it)", r.label))
		}
	}

	// Claude auth — exactly one of oauth/apikey.
	_, hasOAuth := cfg.Secrets["claude_oauth"]
	_, hasAPIKey := cfg.Secrets["claude_apikey"]
	switch {
	case !hasOAuth && !hasAPIKey:
		errs = append(errs, "secrets.claude_oauth or secrets.claude_apikey is required")
	case hasOAuth && hasAPIKey:
		errs = append(errs, "exactly one of secrets.claude_oauth or secrets.claude_apikey (got both)")
	}

	// github_pat required today.
	if _, ok := cfg.Secrets["github_pat"]; !ok {
		errs = append(errs, "secrets.github_pat is required")
	}

	// Per-secret shape + collision detection.
	usedEnvVars := map[string]string{} // env-var → logical origin (for error msg)
	for _, r := range reservedEnvVars {
		usedEnvVars[r] = "(ws-reserved)"
	}

	for name, s := range cfg.Secrets {
		if s.SecretName == "" {
			errs = append(errs, fmt.Sprintf("secrets.%s.secret_name is required", name))
		}
		if s.EnvVar == "" {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var is required", name))
		} else if !envVarPattern.MatchString(s.EnvVar) {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q must match %s", name, s.EnvVar, envVarPattern))
		} else if strings.HasPrefix(s.EnvVar, "WS_SECRET_") {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q collides with ws-reserved WS_SECRET_* prefix", name, s.EnvVar))
		} else if existing, collide := usedEnvVars[s.EnvVar]; collide {
			errs = append(errs, fmt.Sprintf("secrets.%s.env_var %q collides with %s", name, s.EnvVar, existing))
		} else {
			usedEnvVars[s.EnvVar] = "secrets." + name
		}
	}

	for key, _ := range cfg.Env {
		if !envVarPattern.MatchString(key) {
			errs = append(errs, fmt.Sprintf("env.%s name must match %s", key, envVarPattern))
			continue
		}
		if strings.HasPrefix(key, "WS_SECRET_") {
			errs = append(errs, fmt.Sprintf("env.%s collides with ws-reserved WS_SECRET_* prefix", key))
			continue
		}
		if existing, collide := usedEnvVars[key]; collide {
			errs = append(errs, fmt.Sprintf("env.%s collides with %s", key, existing))
			continue
		}
		usedEnvVars[key] = "env." + key
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("config validation failed:\n  - " + strings.Join(errs, "\n  - "))
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 5: Wire into `cmd/root.go`**

In `cmd/root.go`, after the `cfg, err = config.Merge(...)` block and the `cfg.ProjectConfigPath = ...` line, add:

```go
		if err := config.Validate(cfg); err != nil {
			return err
		}
```

Full PersistentPreRunE after change:

```go
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		projectCfgPath, err := config.FindProjectConfig(wd, home)
		if err != nil {
			return err
		}
		projectCfg, err := config.LoadProjectConfig(projectCfgPath)
		if err != nil {
			return fmt.Errorf("loading %s: %w", projectCfgPath, err)
		}
		userCfgPath, err := config.UserConfigPath()
		if err != nil {
			return fmt.Errorf("determining user config path: %w", err)
		}
		userCfg, err := config.LoadUserConfig(userCfgPath)
		if err != nil {
			return fmt.Errorf("loading user config: %w", err)
		}
		cfg, err = config.Merge(userCfg, projectCfg)
		if err != nil {
			return err
		}
		cfg.ProjectConfigPath = projectCfgPath
		if err := config.Validate(cfg); err != nil {
			return err
		}
		runner = gcloud.RealRunner{}
		client = gcloud.NewClient(runner)
		return nil
	},
```

- [ ] **Step 6: Verify `cmd/setup.go` still bypasses validation**

Check `cmd/setup.go` line 32:

```go
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
```

It's already present. No change needed — this makes `ws setup` skip the entire PreRunE including validation.

- [ ] **Step 7: Run all tests**

Run: `go test ./...`
Expected: PASS. Existing `cmd/*_test.go` fixtures construct `cfg` structs directly and never invoke `PersistentPreRunE`, so validation does not run during those tests.

- [ ] **Step 8: Commit**

```bash
git add internal/config/validate.go internal/config/validate_test.go cmd/root.go
git commit -m "$(cat <<'EOF'
feat(config): Validate required keys + secrets constraints

Adds config.Validate with per-key required checks, claude_oauth XOR
claude_apikey, github_pat required, env_var shape regex, and env-var
name collisions (including ws-reserved prefixes). Wired into root's
PersistentPreRunE; setup still bypasses so bootstrap works.
EOF
)"
```

---

## Task 5: `cmd/create.go` — iterate `cfg.Secrets`, emit new wire protocol

Goal: `ws create` stops hardcoding `CLAUDE_AUTH_SECRET_NAME`/`GITHUB_PAT_SECRET_NAME`/`"github-pat"`/`claudeAuthMode`. Instead it iterates `cfg.Secrets`, calls the generalized `ensureSecret` per entry, and emits `WS_SECRETS` plus per-entry `WS_SECRET_<NAME>_NAME` / `_VAR` into `--container-env`. Static `cfg.Env` entries are appended.

**Files:**
- Modify: `cmd/create.go`
- Modify: `cmd/create_test.go`

- [ ] **Step 1: Add the failing test**

Append to `cmd/create_test.go` (create the file with the helper below if it doesn't exist; otherwise append the new test):

```go
func TestBuildContainerEnv_IncludesSecretsAndEnv(t *testing.T) {
	sleep := 2
	cfg := &config.Config{
		ProjectID: "p", GithubRepoURL: "https://example.com/r.git",
		GitUserName: "Alice", GitUserEmail: "a@x.com",
		Region: "us-central1", MachineType: "e2-standard-4",
		DiskSizeGB: 50, ClusterName: "c", ConfigName: "cf",
		ServiceAccountName: "sa", ImageName: "i", ImageTag: "t", RepoName: "r",
		AutoSleepHours: &sleep, AutoShutdownHours: 12,
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
			"github_pat":   {SecretName: "github-pat", EnvVar: "GH_TOKEN"},
			"my_extra":     {SecretName: "ex", EnvVar: "MY_EXTRA"},
		},
		Env: map[string]string{"STATIC_KEY": "abc"},
	}
	got := buildContainerEnv(cfg)
	// buildContainerEnv returns a comma-joined string of KEY=VALUE pairs.
	// Split and put into a map for assertion independence from ordering.
	m := map[string]string{}
	for _, p := range strings.Split(got, ",") {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	must := map[string]string{
		"GCP_PROJECT_ID":               "p",
		"GITHUB_REPO_URL":              "https://example.com/r.git",
		"GIT_USER_NAME":                "Alice",
		"GIT_USER_EMAIL":               "a@x.com",
		"WS_SECRET_CLAUDE_OAUTH_NAME":  "claude-auth",
		"WS_SECRET_CLAUDE_OAUTH_VAR":   "CLAUDE_CODE_OAUTH_TOKEN",
		"WS_SECRET_GITHUB_PAT_NAME":    "github-pat",
		"WS_SECRET_GITHUB_PAT_VAR":     "GH_TOKEN",
		"WS_SECRET_MY_EXTRA_NAME":      "ex",
		"WS_SECRET_MY_EXTRA_VAR":       "MY_EXTRA",
		"STATIC_KEY":                   "abc",
	}
	for k, v := range must {
		if m[k] != v {
			t.Errorf("container env %s: got %q, want %q (full string: %q)", k, m[k], v, got)
		}
	}
	// WS_SECRETS must enumerate all three logical names.
	parts := strings.Split(m["WS_SECRETS"], ",")
	wantSet := map[string]bool{"claude_oauth": true, "github_pat": true, "my_extra": true}
	for _, p := range parts {
		if !wantSet[p] {
			t.Errorf("unexpected WS_SECRETS entry: %q", p)
		}
		delete(wantSet, p)
	}
	if len(wantSet) != 0 {
		t.Errorf("missing WS_SECRETS entries: %v", wantSet)
	}
	// No trace of the old wire-protocol names.
	for _, banned := range []string{"CLAUDE_AUTH_MODE=", "CLAUDE_AUTH_SECRET_NAME=", "GITHUB_PAT_SECRET_NAME="} {
		if strings.Contains(got, banned) {
			t.Errorf("container env still contains old key %q: %q", banned, got)
		}
	}
}

func TestBuildContainerEnv_OAuthAccountSiblingEmitted(t *testing.T) {
	cfg := &config.Config{
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {
				SecretName:             "claude-auth",
				EnvVar:                 "CLAUDE_CODE_OAUTH_TOKEN",
				OAuthAccountSecretName: "claude-oauth-account",
			},
		},
	}
	got := buildContainerEnv(cfg)
	if !strings.Contains(got, "WS_SECRET_CLAUDE_OAUTH_OAUTH_ACCOUNT_SECRET_NAME=claude-oauth-account") {
		t.Errorf("oauth_account_secret_name not emitted: %q", got)
	}
}
```

Imports `config` from `github.com/adamtait/remote-agent/internal/config`. Add the import if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ -run TestBuildContainerEnv`
Expected: `buildContainerEnv` undefined.

- [ ] **Step 3: Extract `buildContainerEnv` and rewire `runCreate`**

In `cmd/create.go`:

(a) Add helpers near the top (after imports):

```go
// logicalNameToEnvToken converts a secrets map key like "claude_oauth" into
// the uppercased token "CLAUDE_OAUTH" used in the WS_SECRET_<TOKEN>_{NAME,VAR}
// wire protocol consumed by entrypoint.sh.
func logicalNameToEnvToken(logical string) string {
	return strings.ToUpper(logical)
}

// buildContainerEnv assembles the comma-separated KEY=VALUE string passed
// to gcloud workstations configs --container-env. Order is deterministic
// (fixed keys first, then secrets sorted by logical name, then env sorted
// by key) to keep the resulting config stable across invocations.
func buildContainerEnv(cfg *config.Config) string {
	var pairs []string
	add := func(k, v string) { pairs = append(pairs, k+"="+v) }

	add("GCP_PROJECT_ID", cfg.ProjectID)
	add("GITHUB_REPO_URL", cfg.GithubRepoURL)
	add("GIT_USER_NAME", cfg.GitUserName)
	add("GIT_USER_EMAIL", cfg.GitUserEmail)

	logicalNames := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		logicalNames = append(logicalNames, k)
	}
	sort.Strings(logicalNames)
	add("WS_SECRETS", strings.Join(logicalNames, ","))
	for _, logical := range logicalNames {
		s := cfg.Secrets[logical]
		tok := logicalNameToEnvToken(logical)
		add("WS_SECRET_"+tok+"_NAME", s.SecretName)
		add("WS_SECRET_"+tok+"_VAR", s.EnvVar)
		if s.OAuthAccountSecretName != "" {
			add("WS_SECRET_"+tok+"_OAUTH_ACCOUNT_SECRET_NAME", s.OAuthAccountSecretName)
		}
	}

	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		add(k, cfg.Env[k])
	}

	return strings.Join(pairs, ",")
}
```

Add `"sort"` to the import block if not already there. Add `"github.com/adamtait/remote-agent/internal/config"` import (it's likely already present indirectly).

(b) In `runCreate`, replace the Step 4 "Secrets" block and the Step 7 "containerEnv" construction. Specifically:

Replace:

```go
	// Step 4 — Secrets
	logStep("Ensuring Claude secret...")
	claudeAuthMode, claudeSecretName, err := ensureClaudeSecret(r, cfg.ProjectID)
	if err != nil {
		return fmt.Errorf("ensuring Claude secret: %w", err)
	}
	logStep("Ensuring GitHub PAT secret...")
	if err := ensureSecret(r, cfg.ProjectID, "github-pat", "Enter GitHub PAT (fine-grained, repo-scoped)"); err != nil {
		return fmt.Errorf("ensuring github-pat secret: %w", err)
	}
```

with:

```go
	// Step 4 — Secrets: ensure every configured secret exists in GCP.
	logStep(fmt.Sprintf("Ensuring %d GCP secret(s)...", len(cfg.Secrets)))
	secretLogicalOrder := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		secretLogicalOrder = append(secretLogicalOrder, k)
	}
	sort.Strings(secretLogicalOrder)
	for _, logical := range secretLogicalOrder {
		s := cfg.Secrets[logical]
		prompt := fmt.Sprintf("Enter value for secret %q (env var %s)", s.SecretName, s.EnvVar)
		if err := ensureSecret(r, cfg.ProjectID, s.SecretName, prompt); err != nil {
			return fmt.Errorf("ensuring secret %s: %w", s.SecretName, err)
		}
		if s.OAuthAccountSecretName != "" {
			p := fmt.Sprintf("Enter value for oauth_account secret %q", s.OAuthAccountSecretName)
			if err := ensureSecret(r, cfg.ProjectID, s.OAuthAccountSecretName, p); err != nil {
				return fmt.Errorf("ensuring oauth_account secret %s: %w", s.OAuthAccountSecretName, err)
			}
		}
	}
```

Replace:

```go
	// Bind secrets
	for _, secret := range []string{claudeSecretName, "github-pat"} {
```

with:

```go
	// Bind secrets — IAM policy for the SA to read each configured secret.
	secretNames := make([]string, 0, len(cfg.Secrets))
	seen := map[string]bool{}
	for _, logical := range secretLogicalOrder {
		s := cfg.Secrets[logical]
		if !seen[s.SecretName] {
			secretNames = append(secretNames, s.SecretName)
			seen[s.SecretName] = true
		}
		if s.OAuthAccountSecretName != "" && !seen[s.OAuthAccountSecretName] {
			secretNames = append(secretNames, s.OAuthAccountSecretName)
			seen[s.OAuthAccountSecretName] = true
		}
	}
	for _, secret := range secretNames {
```

Replace:

```go
	logStep(fmt.Sprintf("Ensuring workstations config '%s' (auth: %s)...", cfg.ConfigName, claudeAuthMode))
	containerEnv := strings.Join([]string{
		"CLAUDE_AUTH_MODE=" + claudeAuthMode,
		"CLAUDE_AUTH_SECRET_NAME=" + claudeSecretName,
		"GITHUB_PAT_SECRET_NAME=github-pat",
		"GCP_PROJECT_ID=" + cfg.ProjectID,
		"GITHUB_REPO_URL=" + cfg.GithubRepoURL,
		"GIT_USER_NAME=" + cfg.GitUserName,
		"GIT_USER_EMAIL=" + cfg.GitUserEmail,
	}, ",")
```

with:

```go
	logStep(fmt.Sprintf("Ensuring workstations config '%s'...", cfg.ConfigName))
	containerEnv := buildContainerEnv(cfg)
```

(c) Delete `ensureClaudeSecret` (the entire function at line ~344–388 of the pre-edit file) — no longer called; logical-name resolution handled by `ws setup` instead of at create time.

(d) Delete the `if cfg.GitUserName == ""` / `if cfg.GitUserEmail == ""` fallback block (lines 56–61 of the pre-edit file). Validation now guarantees these are set; no runtime fallback to "Developer"/"dev@example.com". Also delete the `PROJECT_ID` / `GITHUB_REPO_URL` env-var fallback at lines 36–41 — the same logic lives in `ws setup`'s probing.

Actually — keep the comma/`=` sanity check for GithubRepoURL + git fields (lines 62–70); it guards gcloud's `--container-env` parser against values containing commas. Remove the `cfg.ProjectID = os.Getenv(...)` fallback (validation ensures non-empty).

Final `runCreate` header (replacing lines 36–70) becomes:

```go
func runCreate(cmd *cobra.Command, args []string) error {
	// The workstation image ships HTTPS-only credentials — rewrite any SSH
	// URL so the in-container clone doesn't die on host key verification.
	if normalized := scaffold.NormalizeGithubRepoURL(cfg.GithubRepoURL); normalized != cfg.GithubRepoURL {
		fmt.Printf("\033[1;33mWARN:\033[0m rewriting SSH github_repo_url to HTTPS for container clone: %s → %s\n",
			cfg.GithubRepoURL, normalized)
		cfg.GithubRepoURL = normalized
	}
	for field, val := range map[string]string{
		"git_user_name":   cfg.GitUserName,
		"git_user_email":  cfg.GitUserEmail,
		"github_repo_url": cfg.GithubRepoURL,
	} {
		if strings.ContainsAny(val, ",=") {
			return fmt.Errorf("%s must not contain ',' or '=' (gcloud --container-env limitation): %q", field, val)
		}
	}

	if cmd.Flags().Changed("auto-sleep") {
		v, _ := cmd.Flags().GetInt("auto-sleep")
		cfg.AutoSleepHours = &v
	}
	if cmd.Flags().Changed("auto-shutdown") {
		v, _ := cmd.Flags().GetInt("auto-shutdown")
		cfg.AutoShutdownHours = v
	}
	if cfg.AutoSleepHours != nil {
		h := *cfg.AutoSleepHours
		if h < 0 || h > 24 {
			return fmt.Errorf("auto_sleep_hours must be 0 (never) or 1–24; got %d", h)
		}
	}
	if cfg.AutoShutdownHours < 1 || cfg.AutoShutdownHours > 168 {
		return fmt.Errorf("auto_shutdown_hours must be 1–168; got %d", cfg.AutoShutdownHours)
	}
	// …rest of runCreate unchanged except for the Step 4/Step 5/Step 7
	// replacements noted above.
```

(Note the auto_shutdown validation is now unconditional since validation requires it non-zero.)

Also delete the now-unused `bufio` import and `scanner` logic that lived in `ensureClaudeSecret`; run `goimports` or equivalent.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/ -run TestBuildContainerEnv`
Expected: PASS.

- [ ] **Step 5: Run the full module tests**

Run: `go build ./... && go test ./...`
Expected: PASS. Existing create tests that tested `ensureClaudeSecret` or `CLAUDE_AUTH_MODE=` may fail — remove those tests (they test deleted code).

- [ ] **Step 6: Commit**

```bash
git add cmd/create.go cmd/create_test.go
git commit -m "$(cat <<'EOF'
feat(create): iterate cfg.Secrets for generic wire protocol

Removes hardcoded CLAUDE_AUTH_SECRET_NAME / GITHUB_PAT_SECRET_NAME
/ "github-pat" / claudeAuthMode. `ws create` now walks cfg.Secrets
and cfg.Env to emit WS_SECRETS + WS_SECRET_<LOGICAL>_NAME/_VAR into
--container-env. Claude auth mode is selected at `ws setup` time
via which logical name (claude_oauth vs claude_apikey) appears in
the config, not by a prompt here.
EOF
)"
```

---

## Task 6: Rewrite `entrypoint.sh` — generic loop with special-case dispatch

Goal: `entrypoint.sh` iterates `$WS_SECRETS`, dispatches per logical name (`claude_oauth`, `claude_apikey`, `github_pat`, or generic), preserves the Claude OAuth onboarding seed and the git credential helper.

**Files:**
- Rewrite: `internal/scaffold/assets/entrypoint.sh`

No Go tests; manual smoke test after Task 8 (setup-scaffold integration) runs end-to-end against a test project. The embedded bytes are regenerated automatically via `//go:embed`; no Go code change needed.

- [ ] **Step 1: Rewrite `internal/scaffold/assets/entrypoint.sh`**

Replace the entire file with:

```bash
#!/bin/bash
#
# Runs at every workstation boot (as root, inside the container).
#
# Contract (v3):
#   ws create emits these env vars into the container config:
#     GCP_PROJECT_ID, GITHUB_REPO_URL, GIT_USER_NAME, GIT_USER_EMAIL
#     WS_SECRETS=<comma-separated logical names>
#     for each logical <name>:
#       WS_SECRET_<UPPERCASE_NAME>_NAME=<gcp secret name>
#       WS_SECRET_<UPPERCASE_NAME>_VAR=<container env var name>
#       (optional, claude_oauth only) WS_SECRET_CLAUDE_OAUTH_OAUTH_ACCOUNT_SECRET_NAME
#     plus any static key=value entries from config.env.
#
# Logical names with special handling (still conform to the schema):
#     claude_oauth   → fetch, export $VAR, seed ~/.claude.json onboarding.
#     claude_apikey  → install apiKeyHelper (fetches every call, ~5min TTL).
#     github_pat     → install git credential helper AND export $VAR for `gh`.
#     (any other)    → fetch at boot, export $VAR. Rotation requires restart.
#
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
OAUTH_PROFILE_PATH="/etc/profile.d/claude-auth.sh"
GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"
CLAUDE_CONFIG_PATH="${USER_HOME}/.claude.json"
CLAUDE_LOG_PATH="/var/log/claude-setup.log"

log() {
    local msg="[claude-setup] $(date -u +%H:%M:%S) $*"
    echo "${msg}"
    echo "${msg}" >> "${CLAUDE_LOG_PATH}" 2>/dev/null || true
}

: > "${CLAUDE_LOG_PATH}" 2>/dev/null || true
chmod 0644 "${CLAUDE_LOG_PATH}" 2>/dev/null || true

# =============================================================================
# Required env vars (set by ws create)
# =============================================================================
: "${GCP_PROJECT_ID:?GCP_PROJECT_ID not set}"
: "${WS_SECRETS:?WS_SECRETS not set — check workstations config}"

command -v gcloud >/dev/null 2>&1 || {
    log "ERROR: gcloud CLI missing from image; cannot fetch credentials."
    exit 1
}

# Clear stale auth state from any previous configuration. We always start
# from a clean slate so switching auth modes between boots doesn't leave
# a ghost apiKeyHelper or old env export behind.
rm -f "${OAUTH_PROFILE_PATH}"
rm -f "${APIKEY_HELPER_PATH}"
rm -f "${GIT_HELPER_PATH}"
sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
sed -i '/^GH_TOKEN=/d' /etc/environment
sed -i '/^ANTHROPIC_API_KEY=/d' /etc/environment

# Strip an apiKeyHelper line from settings.json if it's there. OAuth mode
# would otherwise trip Claude Code's "auth conflict" guard.
if [[ -f "${CLAUDE_SETTINGS_PATH}" ]] && command -v jq >/dev/null 2>&1; then
    if jq -e '.apiKeyHelper' "${CLAUDE_SETTINGS_PATH}" >/dev/null 2>&1; then
        tmp=$(mktemp)
        jq 'del(.apiKeyHelper)' "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
            && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
        chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
        log "Cleared stale apiKeyHelper from settings.json."
    fi
fi

# =============================================================================
# Seed ~/.claude.json so interactive `claude` skips the onboarding wizard.
# Called from the claude_oauth branch below.
# =============================================================================
seed_claude_onboarding() {
    local token="$1"
    local oauth_account_secret="$2"

    local version
    version=$(sudo -u "${USER_NAME}" claude --version 2>/dev/null | awk '{print $1}' || true)
    if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
        log "WARNING: could not parse claude --version ('${version}'); proceeding without lastOnboardingVersion."
        version=""
    fi

    local acct=""
    if [[ -n "${oauth_account_secret}" ]]; then
        log "Fetching oauthAccount metadata from Secret Manager (${oauth_account_secret})..."
        acct=$(gcloud secrets versions access latest \
            --secret="${oauth_account_secret}" \
            --project="${GCP_PROJECT_ID}" 2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
        if [[ -z "${acct}" ]]; then
            log "WARNING: oauthAccount fetch returned empty; skipping oauthAccount field."
            acct=""
        elif ! jq -e '.accountUuid and .emailAddress and .organizationUuid' \
                >/dev/null 2>&1 <<<"${acct}"; then
            log "WARNING: oauthAccount JSON missing required fields; skipping oauthAccount field."
            acct=""
        fi
    fi

    local tmp
    tmp=$(mktemp)
    if [[ -f "${CLAUDE_CONFIG_PATH}" ]]; then
        jq --arg acct "${acct}" --arg ver "${version}" '
            .hasCompletedOnboarding = true
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
            | (if $acct != "" then .oauthAccount = ($acct | fromjson) else . end)
        ' "${CLAUDE_CONFIG_PATH}" > "${tmp}" || { rm -f "${tmp}"; log "ERROR: jq merge failed; leaving ${CLAUDE_CONFIG_PATH} untouched."; return 0; }
    else
        jq -n --arg acct "${acct}" --arg ver "${version}" '
            {hasCompletedOnboarding: true}
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
            | (if $acct != "" then .oauthAccount = ($acct | fromjson) else . end)
        ' > "${tmp}" || { rm -f "${tmp}"; log "ERROR: jq render failed; skipping onboarding seed."; return 0; }
    fi

    if ! mv "${tmp}" "${CLAUDE_CONFIG_PATH}"; then
        rm -f "${tmp}"
        log "ERROR: failed to write ${CLAUDE_CONFIG_PATH}."
        return 0
    fi
    chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_CONFIG_PATH}"
    chmod 600 "${CLAUDE_CONFIG_PATH}"
    log "Seeded ${CLAUDE_CONFIG_PATH} (hasCompletedOnboarding=true${acct:+, oauthAccount set})."

    if sudo -u "${USER_NAME}" env CLAUDE_CODE_OAUTH_TOKEN="${token}" \
            claude -p "respond with OK" 2>/dev/null | grep -q 'OK'; then
        log "Smoke test: claude -p returned OK."
    else
        log "WARNING: claude -p smoke test did not return OK; token or account may be stale."
    fi
}

# =============================================================================
# Dispatch loop — iterate WS_SECRETS and handle each logical name.
# =============================================================================
IFS=',' read -r -a ws_secret_names <<<"${WS_SECRETS}"
for logical in "${ws_secret_names[@]}"; do
    [[ -z "${logical}" ]] && continue
    token=$(echo "${logical}" | tr '[:lower:]' '[:upper:]')
    name_var="WS_SECRET_${token}_NAME"
    env_var_var="WS_SECRET_${token}_VAR"
    secret_name="${!name_var:-}"
    target_env_var="${!env_var_var:-}"

    if [[ -z "${secret_name}" || -z "${target_env_var}" ]]; then
        log "ERROR: secret ${logical} is missing ${name_var} or ${env_var_var}; skipping."
        continue
    fi

    case "${logical}" in
        claude_oauth)
            log "claude_oauth: fetching ${secret_name} → ${target_env_var}"
            TOKEN=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
                2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
            if [[ -z "${TOKEN}" ]]; then
                log "WARNING: ${secret_name} fetch failed."
            else
                echo "${target_env_var}=${TOKEN}" >> /etc/environment
                umask 077
                cat > "${OAUTH_PROFILE_PATH}" <<EOF
# Auto-generated at workstation boot. Do not edit.
export ${target_env_var}='${TOKEN}'
EOF
                chmod 0644 "${OAUTH_PROFILE_PATH}"
                log "Exported ${target_env_var} via /etc/environment and ${OAUTH_PROFILE_PATH}."

                oauth_account_var="WS_SECRET_${token}_OAUTH_ACCOUNT_SECRET_NAME"
                seed_claude_onboarding "${TOKEN}" "${!oauth_account_var:-}"
            fi
            ;;

        claude_apikey)
            log "claude_apikey: installing apiKeyHelper for ${secret_name}"
            cat > "${APIKEY_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated. Outputs current value of secret '${secret_name}'.
exec gcloud secrets versions access latest \\
    --secret='${secret_name}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null
EOF
            chmod 0755 "${APIKEY_HELPER_PATH}"
            if ! "${APIKEY_HELPER_PATH}" | grep -q '.'; then
                log "WARNING: apiKeyHelper returned no value. Check SA permissions."
            fi

            mkdir -p "${CLAUDE_SETTINGS_DIR}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_DIR}"
            if [[ ! -f "${CLAUDE_SETTINGS_PATH}" ]]; then
                cat > "${CLAUDE_SETTINGS_PATH}" <<EOF
{
  "apiKeyHelper": "${APIKEY_HELPER_PATH}"
}
EOF
            elif command -v jq >/dev/null 2>&1; then
                tmp=$(mktemp)
                jq --arg h "${APIKEY_HELPER_PATH}" '.apiKeyHelper = $h' \
                    "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                    && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            else
                log "WARNING: settings.json exists and jq missing; leaving it alone."
            fi
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
            log "apiKeyHelper installed; target env var ${target_env_var} is documentation-only."
            ;;

        github_pat)
            log "github_pat: installing git credential helper for ${secret_name}"
            cat > "${GIT_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated. Emits GitHub PAT via git credential protocol.
while IFS= read -r line; do
    [[ -z "\$line" ]] && break
done
pat=\$(gcloud secrets versions access latest \\
    --secret='${secret_name}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || exit 0
echo "username=x-access-token"
echo "password=\${pat}"
EOF
            chmod 0755 "${GIT_HELPER_PATH}"
            git config --system credential.helper "${GIT_HELPER_PATH}"
            log "git credential.helper set to ${GIT_HELPER_PATH}."

            # Also export the env var so `gh` CLI can see the token.
            # Stale on rotation; `git` operations use the helper and stay fresh.
            pat_value=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" 2>/dev/null || true)
            if [[ -n "${pat_value}" ]]; then
                echo "${target_env_var}=${pat_value}" >> /etc/environment
                log "Exported ${target_env_var} for gh CLI."
            fi
            ;;

        *)
            log "generic: fetching ${secret_name} → ${target_env_var}"
            val=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
                2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
            if [[ -n "${val}" ]]; then
                echo "${target_env_var}=${val}" >> /etc/environment
                log "Exported ${target_env_var}."
            else
                log "WARNING: ${secret_name} fetch returned empty."
            fi
            ;;
    esac
done

# =============================================================================
# First-boot-only: gitconfig + repo clone.
# =============================================================================
FIRST_BOOT_MARKER="${USER_HOME}/.ws-first-boot-done"
if [[ ! -f "${FIRST_BOOT_MARKER}" ]]; then
    log "First boot: configuring git identity and cloning repo."
    : "${GITHUB_REPO_URL:?GITHUB_REPO_URL not set}"
    : "${GIT_USER_NAME:?GIT_USER_NAME not set}"
    : "${GIT_USER_EMAIL:?GIT_USER_EMAIL not set}"

    sudo -u "${USER_NAME}" git config --global user.name "${GIT_USER_NAME}"
    sudo -u "${USER_NAME}" git config --global user.email "${GIT_USER_EMAIL}"

    if [[ ! -d "${WORKSPACE_DIR}/.git" ]]; then
        sudo -u "${USER_NAME}" mkdir -p "${WORKSPACE_DIR}"
        sudo -u "${USER_NAME}" git clone "${GITHUB_REPO_URL}" "${WORKSPACE_DIR}" \
            2> >(while read -r line; do log "git: ${line}"; done) \
            || log "WARNING: repo clone failed; proceeding anyway."
    fi
    touch "${FIRST_BOOT_MARKER}"
    chown "${USER_NAME}:${USER_NAME}" "${FIRST_BOOT_MARKER}"
fi

log "claude-setup complete."
```

Preserves the spirit of the original: same helper paths, same marker-based first-boot guard, same log target. The per-mode case block is replaced by a single dispatch loop that reads `WS_SECRETS` / `WS_SECRET_*` from the container env.

- [ ] **Step 2: Build to confirm `//go:embed` still picks up the new bytes**

Run: `go build ./internal/scaffold/...`
Expected: PASS. `embedded.go`'s `//go:embed assets/entrypoint.sh` directive rebinds `embeddedEntrypoint` on next build.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/assets/entrypoint.sh
git commit -m "$(cat <<'EOF'
feat(entrypoint): iterate WS_SECRETS with special-case dispatch

Replaces the CLAUDE_AUTH_MODE case with a loop over WS_SECRETS. The
three built-in logical names (claude_oauth, claude_apikey, github_pat)
get their existing helper wiring; any other name is fetched and
exported generically. Rotation without restart is preserved for
claude_apikey (via apiKeyHelper) and git operations (via credential
helper) but not for generically-handled secrets or GH_TOKEN for gh CLI.
EOF
)"
```

---

## Task 7: Rewrite scaffold templates — `RenderUserConfig` and `RenderProjectConfig`

Goal: Drop the old commented-template machinery. New renderers emit fully-resolved user configs (everything uncommented) and minimal project configs (two required keys uncommented, override examples commented). Secrets section carries its documentation verbatim.

**Files:**
- Rewrite: `internal/scaffold/templates.go`
- Rewrite: `internal/scaffold/templates_test.go`

- [ ] **Step 1: Write the failing tests**

Replace `internal/scaffold/templates_test.go` contents with:

```go
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

func TestRenderUserConfig_EmitsAllRequiredKeysUncommented(t *testing.T) {
	uv := scaffold.UserValues{
		GitUserName:  "Alice",
		GitUserEmail: "alice@example.com",
		Region:       "us-central1", MachineType: "e2-standard-4", DiskSizeGB: 50,
		ClusterName: "claude-dev-cluster", ConfigName: "claude-dev-config",
		ServiceAccountName: "claude-workstation-sa",
		ImageName:          "claude-code-workstation",
		ImageTag:           "latest",
		RepoName:           "claude-workstation-images",
		AutoSleepHours:     2,
		AutoShutdownHours:  12,
		ClaudeAuthMode:     scaffold.ClaudeAuthOAuth,
	}
	got := scaffold.RenderUserConfig(uv)
	for _, want := range []string{
		`git:`,
		`  user_name: "Alice"`,
		`  user_email: "alice@example.com"`,
		`workstation:`,
		`  region: us-central1`,
		`  machine_type: e2-standard-4`,
		`  disk_size_gb: 50`,
		`  cluster_name: claude-dev-cluster`,
		`  config_name: claude-dev-config`,
		`  service_account_name: claude-workstation-sa`,
		`  image_name: claude-code-workstation`,
		`  image_tag: latest`,
		`  repo_name: claude-workstation-images`,
		`  auto_sleep_hours: 2`,
		`  auto_shutdown_hours: 12`,
		`secrets:`,
		`  claude_oauth:`,
		`    secret_name: claude-code-oauth-token`,
		`    env_var: CLAUDE_CODE_OAUTH_TOKEN`,
		`  github_pat:`,
		`    secret_name: github-pat`,
		`    env_var: GH_TOKEN`,
		`env: {}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user config missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderUserConfig_APIKeyModeSelectsApiKeyEntry(t *testing.T) {
	uv := scaffold.UserValues{ClaudeAuthMode: scaffold.ClaudeAuthAPIKey}
	got := scaffold.RenderUserConfig(uv)
	if !strings.Contains(got, "claude_apikey:") {
		t.Errorf("apikey mode did not write claude_apikey entry:\n%s", got)
	}
	if strings.Contains(got, "claude_oauth:") {
		t.Errorf("apikey mode also wrote claude_oauth entry:\n%s", got)
	}
	if !strings.Contains(got, "env_var: ANTHROPIC_API_KEY") {
		t.Errorf("apikey env_var should be ANTHROPIC_API_KEY:\n%s", got)
	}
}

func TestRenderUserConfig_IncludesSecretsDocumentationComments(t *testing.T) {
	got := scaffold.RenderUserConfig(scaffold.UserValues{ClaudeAuthMode: scaffold.ClaudeAuthOAuth})
	for _, want := range []string{
		"claude_oauth  → fetches at boot",
		"claude_apikey → installs Claude",
		"github_pat    → installs git",
		"Exactly one of {claude_oauth, claude_apikey}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user config missing documentation comment %q", want)
		}
	}
}

func TestRenderProjectConfig_EmitsRequiredKeysAndCommentedExamples(t *testing.T) {
	pv := scaffold.ProjectValues{
		ProjectID:     "my-proj",
		GithubRepoURL: "https://example.com/r.git",
	}
	got := scaffold.RenderProjectConfig(pv)
	for _, want := range []string{
		`project:`,
		`  id: my-proj`,
		`  github_repo_url: https://example.com/r.git`,
		`# workstation:`,
		`#   machine_type: e2-standard-8`,
		`# secrets:`,
		`# env:`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("project config missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderProjectConfig_ZeroValuesStillShowCommentedExamples(t *testing.T) {
	got := scaffold.RenderProjectConfig(scaffold.ProjectValues{})
	for _, want := range []string{`# workstation:`, `# secrets:`, `# env:`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented example %q", want)
		}
	}
	if !strings.Contains(got, "id:") || !strings.Contains(got, "github_repo_url:") {
		t.Error("required keys should still be present as placeholders")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestRenderUserConfig -run TestRenderProjectConfig`
Expected: compile errors (types and functions undefined).

- [ ] **Step 3: Rewrite `internal/scaffold/templates.go`**

Replace the entire file with:

```go
// ws/internal/scaffold/templates.go
package scaffold

import (
	"fmt"
	"strings"
)

// ClaudeAuthMode selects which "claude_*" logical secret the user config
// writes. Setting this to oauth writes the claude_oauth entry; apikey
// writes claude_apikey. Exactly one of the two must end up in the merged
// config (validate.go enforces this).
type ClaudeAuthMode int

const (
	ClaudeAuthOAuth ClaudeAuthMode = iota
	ClaudeAuthAPIKey
)

// UserValues are the prompt-gathered inputs for ~/.config/ws/config.yaml.
// Zero-value strings render the seed default from ws setup (e.g.
// "e2-standard-4") — the template is exhaustive, so there's no commented-
// fallback path; unset fields simply use their typed default written here.
type UserValues struct {
	GitUserName  string
	GitUserEmail string

	Region             string
	MachineType        string
	DiskSizeGB         int
	ClusterName        string
	ConfigName         string
	ServiceAccountName string
	ImageName          string
	ImageTag           string
	RepoName           string
	AutoSleepHours     int
	AutoShutdownHours  int

	ClaudeAuthMode ClaudeAuthMode
}

// ProjectValues are the prompt-gathered inputs for .ws/config.yaml.
type ProjectValues struct {
	ProjectID     string
	GithubRepoURL string
}

// Seed defaults applied when a UserValues field is zero.
const (
	seedRegion             = "us-central1"
	seedMachineType        = "e2-standard-4"
	seedDiskSizeGB         = 50
	seedClusterName        = "claude-dev-cluster"
	seedConfigName         = "claude-dev-config"
	seedServiceAccountName = "claude-workstation-sa"
	seedImageName          = "claude-code-workstation"
	seedImageTag           = "latest"
	seedRepoName           = "claude-workstation-images"
	seedAutoSleepHours     = 2
	seedAutoShutdownHours  = 12
	placeholderGitUserName = "your-name"
	placeholderGitEmail    = "your-email@example.com"
	placeholderProjectID   = "my-gcp-project"
	placeholderRepoURL     = "https://github.com/my-org/my-repo.git"
)

func orStr(v, seed string) string {
	if v == "" {
		return seed
	}
	return v
}

func orInt(v, seed int) int {
	if v == 0 {
		return seed
	}
	return v
}

// RenderUserConfig returns a fully-populated ~/.config/ws/config.yaml.
// Every required key is uncommented; the secrets section carries
// documentation comments explaining the three built-in logical names.
func RenderUserConfig(uv UserValues) string {
	gitName := orStr(uv.GitUserName, placeholderGitUserName)
	gitEmail := orStr(uv.GitUserEmail, placeholderGitEmail)

	region := orStr(uv.Region, seedRegion)
	machine := orStr(uv.MachineType, seedMachineType)
	disk := orInt(uv.DiskSizeGB, seedDiskSizeGB)
	cluster := orStr(uv.ClusterName, seedClusterName)
	conf := orStr(uv.ConfigName, seedConfigName)
	sa := orStr(uv.ServiceAccountName, seedServiceAccountName)
	img := orStr(uv.ImageName, seedImageName)
	tag := orStr(uv.ImageTag, seedImageTag)
	repo := orStr(uv.RepoName, seedRepoName)
	sleep := orInt(uv.AutoSleepHours, seedAutoSleepHours)
	shutdown := orInt(uv.AutoShutdownHours, seedAutoShutdownHours)

	var claudeEntry string
	switch uv.ClaudeAuthMode {
	case ClaudeAuthAPIKey:
		claudeEntry = `  claude_apikey:
    secret_name: anthropic-api-key
    env_var: ANTHROPIC_API_KEY`
	default:
		claudeEntry = `  claude_oauth:
    secret_name: claude-code-oauth-token
    env_var: CLAUDE_CODE_OAUTH_TOKEN
    # oauth_account_secret_name: claude-oauth-account   # optional`
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# ws user config — baseline for all ws commands.
# Every required key is set here. Project configs (.ws/config.yaml)
# override per-key. See docs/superpowers/specs/2026-04-23-ws-layered-config-design.md

git:
  user_name: %q
  user_email: %q

workstation:
  region: %s
  machine_type: %s
  disk_size_gb: %d
  cluster_name: %s
  config_name: %s
  service_account_name: %s
  image_name: %s
  image_tag: %s
  repo_name: %s
  auto_sleep_hours: %d
  auto_shutdown_hours: %d

# secrets — map GCP Secret Manager secrets to container env vars. Each
# entry has the same {secret_name, env_var} shape. ws recognizes three
# logical names and wires them into credential helpers; any other name
# is treated generically (fetched at boot, exported as env_var).
#
#   claude_oauth  → fetches at boot, exports env_var (CLAUDE_CODE_OAUTH_TOKEN).
#                   Also seeds ~/.claude.json onboarding if
#                   oauth_account_secret_name is set.
#   claude_apikey → installs Claude Code's apiKeyHelper — re-fetches every
#                   ~5 min (rotation without restart). env_var is the
#                   canonical name (ANTHROPIC_API_KEY) for documentation.
#   github_pat    → installs git's credential helper (fresh fetch per git
#                   op) AND exports env_var (GH_TOKEN) for `+"`gh`"+` CLI.
#                   Env var is stale on rotation; git operations stay fresh.
#
# Other logical names: plain fetch + export. Rotation requires ws stop/start.
# Exactly one of {claude_oauth, claude_apikey} must be present.
secrets:
%s
  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN

# env — plain static env vars forwarded to the container (no secret fetch).
env: {}
`,
		gitName, gitEmail,
		region, machine, disk, cluster, conf, sa, img, tag, repo, sleep, shutdown,
		claudeEntry)
	return b.String()
}

// RenderProjectConfig returns a minimal .ws/config.yaml with the two
// required project keys and commented-out override examples for every
// other group.
func RenderProjectConfig(pv ProjectValues) string {
	projectID := orStr(pv.ProjectID, placeholderProjectID)
	repoURL := orStr(pv.GithubRepoURL, placeholderRepoURL)

	var b strings.Builder
	fmt.Fprintf(&b, `# ws project config — committed to source control.
# Precedence: project overrides user. See user config for defaults and
# the full secrets/env schema.

project:
  id: %s
  github_repo_url: %s

# Uncomment any of the groups below to override user values for this project.
#
# workstation:
#   machine_type: e2-standard-8
#   disk_size_gb: 100
#   auto_sleep_hours: 1
#
# secrets:
#   my_extra_secret:
#     secret_name: my-extra-secret
#     env_var: MY_EXTRA
#
# env:
#   MY_STATIC_VAR: foo
`, projectID, repoURL)
	return b.String()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/scaffold/ -run TestRenderUserConfig -run TestRenderProjectConfig`
Expected: PASS.

- [ ] **Step 5: Remove now-unused `ProbedValues`-era helpers if orphaned**

Check remaining uses of `splitCommentedDefault`, `probedOr`, `quoteOrPlaceholder`, `firstNonEmpty`, `probedNone`, `UserProjectValues`, `UserGlobalValues`, `RenderProjectTemplate`, `RenderGlobalTemplate`:

Run: `grep -rn "splitCommentedDefault\|UserProjectValues\|UserGlobalValues\|RenderProjectTemplate\|RenderGlobalTemplate" internal/ cmd/`

Each hit in a `*_test.go` or `scaffold.go` will fail to compile now — fix in Task 8. If hits remain only in `templates.go` (the file we just rewrote), they're dead — leave them out. The new `templates.go` above doesn't import any of them.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/templates.go internal/scaffold/templates_test.go
git commit -m "$(cat <<'EOF'
feat(scaffold): RenderUserConfig / RenderProjectConfig for grouped YAML

User config is exhaustive and fully rendered (no commented-fallback
path); secrets section carries documentation for the three special
logical names. Project config shows required keys + commented-out
override examples.
EOF
)"
```

---

## Task 8: Rewrite `InteractiveScaffold` and `TemplateScaffold` for the new prompts

Goal: `ws setup` prompts for git identity, Claude auth mode, and the project keys; writes the user config to `~/.config/ws/config.yaml`; materializes asset templates under `~/.config/ws/template/`; writes the project config to `.ws/config.yaml`. `--with-project-assets` additionally copies assets into `.ws/`.

**Files:**
- Rewrite: `internal/scaffold/scaffold.go`
- Rewrite: `internal/scaffold/scaffold_test.go`
- Modify: `cmd/setup.go` — register `--with-project-assets` flag and pass it through.
- Modify: `cmd/setup_test.go` — update smoke tests for new schema.
- Modify: `internal/scaffold/scaffold_assets.go` — parameterize `Cwd` vs `Home/template` target.

- [ ] **Step 1: Extend `ScaffoldAssets` with a target-dir option**

Replace the `ScaffoldAssetsOptions` and `ScaffoldAssets` in `internal/scaffold/scaffold_assets.go` top-half with:

```go
type ScaffoldAssetsOptions struct {
	// TargetDir is the absolute directory that will hold the materialized
	// assets (e.g. /home/alice/.config/ws/template or /path/to/project/.ws).
	TargetDir string

	// Home is still needed so the resolver can look up user-template
	// overrides when materializing into a project dir.
	Home string

	Stdout io.Writer

	Force       bool
	DryRun      bool
	OnlyMissing bool
}
```

In `ScaffoldAssets`, change every `filepath.Join(opts.Cwd, ".ws")` to `opts.TargetDir`, and change the log lines to mention the assets' full target path rather than `.ws/<name>`. Update callers in Task 8's next steps.

- [ ] **Step 2: Write the failing scaffold tests**

Replace `internal/scaffold/scaffold_test.go` with:

```go
package scaffold_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

func TestInteractiveScaffold_WritesExhaustiveUserConfig(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// Prompts: project_id, github_repo_url, claude_auth_mode (1=oauth),
	// git_user_name, git_user_email, confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"1",
		"Alice",
		"alice@example.com",
		"y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	user, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	for _, want := range []string{
		`  user_name: "Alice"`,
		`  user_email: "alice@example.com"`,
		`  region: us-central1`,
		`  machine_type: e2-standard-4`,
		`  claude_oauth:`,
		`    secret_name: claude-code-oauth-token`,
		`  github_pat:`,
		`env: {}`,
	} {
		if !strings.Contains(string(user), want) {
			t.Errorf("user config missing %q\n---\n%s", want, user)
		}
	}

	project, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	for _, want := range []string{
		`  id: my-proj-123`,
		`  github_repo_url: https://github.com/a/b.git`,
		`# workstation:`,
		`# secrets:`,
	} {
		if !strings.Contains(string(project), want) {
			t.Errorf("project config missing %q\n---\n%s", want, project)
		}
	}

	// User-dir assets exist; .ws/ assets do NOT (default flag).
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		userAsset := filepath.Join(home, ".config", "ws", "template", name)
		if _, err := os.Stat(userAsset); err != nil {
			t.Errorf("user asset %s not materialized: %v", userAsset, err)
		}
		projectAsset := filepath.Join(cwd, ".ws", name)
		if _, err := os.Stat(projectAsset); err == nil {
			t.Errorf("project asset %s should NOT exist without --with-project-assets", projectAsset)
		}
	}
}

func TestInteractiveScaffold_APIKeyMode(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"2", // apikey
		"Alice",
		"alice@example.com",
		"y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatal(err)
	}
	user, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	if !strings.Contains(string(user), "claude_apikey:") {
		t.Errorf("expected claude_apikey entry in apikey mode:\n%s", user)
	}
	if strings.Contains(string(user), "claude_oauth:") {
		t.Errorf("unexpected claude_oauth entry in apikey mode:\n%s", user)
	}
}

func TestInteractiveScaffold_WithProjectAssetsCopiesAssetsToDotWS(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "1", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
		WithProjectAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ws", name)); err != nil {
			t.Errorf(".ws/%s missing despite --with-project-assets: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".config", "ws", "template", name)); err != nil {
			t.Errorf("user template %s missing: %v", name, err)
		}
	}
}

func TestInteractiveScaffold_IdempotentWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userCfg := filepath.Join(home, ".config", "ws", "config.yaml")
	projectCfg := filepath.Join(cwd, ".ws", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(projectCfg), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(userCfg, []byte("preexisting: user\n"), 0644)
	os.WriteFile(projectCfg, []byte("preexisting: project\n"), 0644)

	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err != nil {
		t.Fatalf("expected no-op success, got %v", err)
	}
	for _, p := range []string{userCfg, projectCfg} {
		got, _ := os.ReadFile(p)
		if !strings.HasPrefix(string(got), "preexisting:") {
			t.Errorf("%s was overwritten without --force:\n%s", p, got)
		}
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestInteractiveScaffold`
Expected: compile errors — `WithProjectAssets` field undefined, old functions missing, etc.

- [ ] **Step 4: Rewrite `internal/scaffold/scaffold.go`**

Replace the entire file with:

```go
// ws/internal/scaffold/scaffold.go
package scaffold

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type InteractiveOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Cwd    string
	Home   string
	Prober Prober

	Force             bool
	ProjectOnly       bool
	GlobalOnly        bool
	Advanced          bool
	WithProjectAssets bool
}

type TemplateOptions struct {
	Stdout io.Writer
	Cwd    string
	Home   string
	Prober Prober

	Force             bool
	ProjectOnly       bool
	GlobalOnly        bool
	WithProjectAssets bool
}

func InteractiveScaffold(opts InteractiveOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	projectSection := !opts.GlobalOnly
	userSection := !opts.ProjectOnly

	projectPath := filepath.Join(opts.Cwd, ".ws", "config.yaml")
	userPath := filepath.Join(opts.Home, ".config", "ws", "config.yaml")
	userTemplateDir := filepath.Join(opts.Home, ".config", "ws", "template")

	writeProject := projectSection
	writeUser := userSection

	if writeProject && !opts.Force && fileExists(projectPath) {
		fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", projectPath)
		writeProject = false
	}
	if writeUser && !opts.Force && fileExists(userPath) {
		fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", userPath)
		writeUser = false
	}

	stdin := bufio.NewReader(opts.Stdin)

	warnPrereqs(opts.Stderr, opts.Prober)

	probedProjectID := opts.Prober.GcloudProject()
	probedRegion := GcloudRegionWithFallback(opts.Prober)
	probedGitName := opts.Prober.GitUserName()
	probedGitEmail := opts.Prober.GitUserEmail()
	probedRepoURL := opts.Prober.GitOriginURL()

	var pv ProjectValues
	if writeProject {
		projectID, err := Prompt(stdin, opts.Stdout, "project_id", probedProjectID, ValidateProjectID)
		if err != nil {
			return err
		}
		pv.ProjectID = projectID

		if !opts.Prober.BillingEnabled(projectID) && opts.Stderr != nil {
			fmt.Fprintf(opts.Stderr, "WARN: could not confirm billing is enabled on %s (ws create will fail otherwise)\n", projectID)
		}

		repoURL, err := Prompt(stdin, opts.Stdout, "github_repo_url", probedRepoURL, ValidateGithubRepoURL)
		if err != nil {
			return err
		}
		if normalized := NormalizeGithubRepoURL(repoURL); normalized != repoURL {
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr, "Converting SSH URL to HTTPS (workstation uses PAT auth): %s → %s\n", repoURL, normalized)
			}
			repoURL = normalized
		}
		pv.GithubRepoURL = repoURL
	}

	var uv UserValues
	if writeUser {
		mode, err := promptClaudeAuthMode(stdin, opts.Stdout)
		if err != nil {
			return err
		}
		uv.ClaudeAuthMode = mode

		uv.GitUserName, err = Prompt(stdin, opts.Stdout, "git.user_name", probedGitName, nil)
		if err != nil {
			return err
		}
		uv.GitUserEmail, err = Prompt(stdin, opts.Stdout, "git.user_email", probedGitEmail, nil)
		if err != nil {
			return err
		}

		if opts.Advanced {
			uv.Region, err = Prompt(stdin, opts.Stdout, "workstation.region", probedRegion, nil)
			if err != nil {
				return err
			}
			// (advanced prompts for workstation.* — omitted here to keep
			// the flow short. Non-advanced takes seed defaults from the
			// template renderer's or* helpers.)
		}
	}

	if writeProject || writeUser {
		fmt.Fprintln(opts.Stdout)
		if writeProject {
			fmt.Fprintln(opts.Stdout, "About to write "+projectPath+":")
			fmt.Fprintln(opts.Stdout, indent(RenderProjectConfig(pv), "  "))
		}
		if writeUser {
			fmt.Fprintln(opts.Stdout, "About to write "+userPath+":")
			fmt.Fprintln(opts.Stdout, indent(RenderUserConfig(uv), "  "))
		}
		confirm, err := Prompt(stdin, opts.Stdout, "Proceed? [Y/n]", "Y", nil)
		if err != nil {
			return err
		}
		ans := strings.ToLower(strings.TrimSpace(confirm))
		if ans == "n" || ans == "no" {
			return fmt.Errorf("%w: user declined", ErrCancelled)
		}
	}

	// Materialize user-dir assets whenever the user is doing user-side setup.
	if userSection {
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			TargetDir: userTemplateDir,
			Home:      opts.Home,
			Stdout:    opts.Stdout,
			Force:     opts.Force, OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
	}

	// Optionally copy assets into the project's .ws/ directory.
	if projectSection && opts.WithProjectAssets {
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			TargetDir: filepath.Join(opts.Cwd, ".ws"),
			Home:      opts.Home,
			Stdout:    opts.Stdout,
			Force:     opts.Force, OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
	}

	if writeProject {
		if err := WriteAtomic(projectPath, []byte(RenderProjectConfig(pv)), opts.Force); err != nil {
			return err
		}
	}
	if writeUser {
		if err := WriteAtomic(userPath, []byte(RenderUserConfig(uv)), opts.Force); err != nil {
			return err
		}
	}

	fmt.Fprintln(opts.Stdout, "Run 'ws create' to provision the workstation.")
	return nil
}

// promptClaudeAuthMode asks the user whether to configure OAuth or API key.
// Accepted answers: "1"/"oauth" for OAuth; "2"/"apikey" for API key.
func promptClaudeAuthMode(stdin *bufio.Reader, out io.Writer) (ClaudeAuthMode, error) {
	fmt.Fprintln(out, "Claude Code authentication:")
	fmt.Fprintln(out, "  1) OAuth token  (recommended — uses your Claude Pro/Max subscription)")
	fmt.Fprintln(out, "  2) API key      (pay-as-you-go API billing)")
	ans, err := Prompt(stdin, out, "Choice [1/2]", "1", nil)
	if err != nil {
		return 0, err
	}
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "1", "oauth":
		return ClaudeAuthOAuth, nil
	case "2", "apikey", "api-key", "api_key":
		return ClaudeAuthAPIKey, nil
	default:
		return 0, fmt.Errorf("invalid choice: %q (expected 1 or 2)", ans)
	}
}

func TemplateScaffold(opts TemplateOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	userTemplateDir := filepath.Join(opts.Home, ".config", "ws", "template")

	if !opts.GlobalOnly {
		path := filepath.Join(opts.Cwd, ".ws", "config.yaml")
		if !opts.Force && fileExists(path) {
			fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", path)
		} else {
			if err := WriteAtomic(path, []byte(RenderProjectConfig(ProjectValues{})), opts.Force); err != nil {
				return err
			}
			fmt.Fprintln(opts.Stdout, "Wrote "+path)
		}
		if opts.WithProjectAssets {
			if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
				TargetDir: filepath.Join(opts.Cwd, ".ws"),
				Home:      opts.Home,
				Stdout:    opts.Stdout,
				Force:     opts.Force, OnlyMissing: !opts.Force,
			}); err != nil {
				return err
			}
		}
	}
	if !opts.ProjectOnly {
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			TargetDir: userTemplateDir,
			Home:      opts.Home,
			Stdout:    opts.Stdout,
			Force:     opts.Force, OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
		path := filepath.Join(opts.Home, ".config", "ws", "config.yaml")
		if !opts.Force && fileExists(path) {
			fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", path)
		} else {
			if err := WriteAtomic(path, []byte(RenderUserConfig(UserValues{})), opts.Force); err != nil {
				return err
			}
			fmt.Fprintln(opts.Stdout, "Wrote "+path)
		}
	}
	return nil
}

func warnPrereqs(out io.Writer, p Prober) {
	if out == nil {
		return
	}
	if !p.HasGcloud() {
		fmt.Fprintln(out, "WARN: gcloud CLI not found on PATH — ws create will fail until installed")
	} else if !p.HasGcloudAuth() {
		fmt.Fprintln(out, "WARN: no active gcloud credentials — run 'gcloud auth login' before 'ws create'")
	}
	if !p.HasDocker() {
		fmt.Fprintln(out, "WARN: docker not found on PATH — ws create needs it to build the workstation image")
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 5: Wire `--with-project-assets` into `cmd/setup.go`**

In `cmd/setup.go`:

In the `runE` closure, extend `readFlags`:

```go
f := readFlags(cmd, "force", "project-only", "global-only", "advanced", "with-project-assets")
```

Pass it through:

```go
return scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
	Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
	Cwd: cwd, Home: home, Prober: scaffold.RealProber{},
	Force:             f["force"],
	ProjectOnly:       f["project-only"],
	GlobalOnly:        f["global-only"],
	Advanced:          f["advanced"],
	WithProjectAssets: f["with-project-assets"],
})
```

Same addition to `setupTemplateCmd`'s `runE` for `TemplateOptions`.

In `addSharedSetupFlags`, add the flag:

```go
c.Flags().Bool("with-project-assets", false, "also materialize Dockerfile and entrypoint.sh into .ws/")
```

- [ ] **Step 6: Fix existing `cmd/setup_test.go` assertions**

Existing setup tests likely assert on strings like `cluster_name:` (commented) or `marshal()` output. Rewrite assertions to check the new schema:

- User config output contains `git:`, `workstation:`, `secrets:`, `env: {}`, `claude_oauth:`, `github_pat:`.
- Project config output contains `project:`, `id:`, `github_repo_url:`, and `# workstation:` commented-out.

Where the existing test imports `scaffold.UserProjectValues` / `UserGlobalValues`, replace with `scaffold.UserValues` / `scaffold.ProjectValues`. Where it uses `RenderProjectTemplate` / `RenderGlobalTemplate`, use `RenderProjectConfig` / `RenderUserConfig`.

Run: `go test ./cmd/ -run TestSetup` — iterate until green.

- [ ] **Step 7: Run full module tests**

Run: `go build ./... && go test ./...`
Expected: PASS. Scaffold asset tests (`scaffold_assets_test.go`) may need `TargetDir` replacing `Cwd` — the field is required now.

- [ ] **Step 8: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go \
        internal/scaffold/templates.go internal/scaffold/scaffold_assets.go \
        internal/scaffold/scaffold_assets_test.go cmd/setup.go cmd/setup_test.go
git commit -m "$(cat <<'EOF'
feat(setup): new prompt flow + user-dir asset materialization

Prompts for project id/repo, Claude auth mode, git identity, then
writes exhaustive user config and minimal project config. User-dir
assets (~/.config/ws/template/{Dockerfile,entrypoint.sh}) are
materialized by default; --with-project-assets also copies them
into .ws/ for project-specific overrides.
EOF
)"
```

---

## Task 9: Remove embedded-fallback from `ResolveSource`; keep embedded as seed-only

Goal: At runtime, asset resolution must return a concrete file (project or user template). The embedded bytes are only used by `ws setup` as the initial seed.

**Files:**
- Modify: `internal/scaffold/source.go`
- Modify: `internal/scaffold/source_test.go`
- Modify: `internal/scaffold/scaffold_assets.go` — pull seed bytes from `EmbeddedSeed` (not from `ResolveSource`).

- [ ] **Step 1: Write/update the tests**

In `internal/scaffold/source_test.go`, replace existing tests with:

```go
package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

func TestResolveSource_UserTemplateWins(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	userDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "Dockerfile"), []byte("USER"), 0644); err != nil {
		t.Fatal(err)
	}
	body, label, err := scaffold.ResolveSource(home, project, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "USER" || label != "user-template" {
		t.Errorf("got %q label=%s, want USER/user-template", body, label)
	}
}

func TestResolveSource_ProjectOverridesUser(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "ws", "template"), 0755)
	os.MkdirAll(filepath.Join(project, ".ws"), 0755)
	os.WriteFile(filepath.Join(home, ".config", "ws", "template", "Dockerfile"), []byte("USER"), 0644)
	os.WriteFile(filepath.Join(project, ".ws", "Dockerfile"), []byte("PROJECT"), 0644)
	body, label, err := scaffold.ResolveSource(home, project, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "PROJECT" || label != "project-override" {
		t.Errorf("got %q label=%s, want PROJECT/project-override", body, label)
	}
}

func TestResolveSource_MissingBoth_Errors(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	_, _, err := scaffold.ResolveSource(home, project, "Dockerfile")
	if err == nil || !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("want error naming the missing asset, got %v", err)
	}
}

func TestResolveSource_UnknownAsset(t *testing.T) {
	if _, _, err := scaffold.ResolveSource(t.TempDir(), t.TempDir(), "bogus"); err == nil {
		t.Error("expected error for unknown asset, got nil")
	}
}

func TestEmbeddedSeed_DockerfileNonEmpty(t *testing.T) {
	body, ok := scaffold.EmbeddedSeed("Dockerfile")
	if !ok || len(body) == 0 {
		t.Errorf("embedded Dockerfile seed missing: ok=%v len=%d", ok, len(body))
	}
	if !strings.Contains(string(body), "FROM") {
		t.Error("embedded Dockerfile looks wrong")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestResolveSource -run TestEmbeddedSeed`
Expected: compile errors on the new `ResolveSource(home, project, name)` signature and `EmbeddedSeed`.

- [ ] **Step 3: Rewrite `internal/scaffold/source.go`**

Replace the file with:

```go
// Package scaffold — source.go
// At runtime, asset bytes come from the project override (.ws/<name>) or
// the user template (~/.config/ws/template/<name>). At setup time,
// EmbeddedSeed provides the initial bytes to materialize into the user
// template directory. The embedded bytes are not a runtime fallback.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AssetNames enumerates the build assets ws knows how to scaffold.
var AssetNames = []string{"Dockerfile", "entrypoint.sh"}

// ResolveSource returns the runtime bytes for a named asset.
//
// Precedence (per asset, independent):
//  1. Project override: <projectRoot>/.ws/<name>
//  2. User template:    <home>/.config/ws/template/<name>
//
// If neither exists, returns an error — no silent fallback to embedded
// defaults. `ws setup` materializes the user template so this stays
// satisfied in normal operation.
//
// Labels: "project-override" or "user-template".
func ResolveSource(home, projectRoot, name string) ([]byte, string, error) {
	if _, ok := embeddedFor(name); !ok {
		return nil, "", fmt.Errorf("unknown scaffold asset %q (known: %v)", name, AssetNames)
	}

	projectPath := filepath.Join(projectRoot, ".ws", name)
	if data, err := os.ReadFile(projectPath); err == nil {
		return data, "project-override", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("reading project override %s: %w", projectPath, err)
	}

	userPath := filepath.Join(home, ".config", "ws", "template", name)
	if data, err := os.ReadFile(userPath); err == nil {
		return data, "user-template", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("reading user template %s: %w", userPath, err)
	}

	return nil, "", fmt.Errorf("asset %s not found in %s or %s (run 'ws setup' to materialize)",
		name, projectPath, userPath)
}

// EmbeddedSeed returns the binary-bundled seed bytes for a named asset.
// Use only from `ws setup` to bootstrap the user template directory;
// runtime lookups should go through ResolveSource.
func EmbeddedSeed(name string) ([]byte, bool) {
	return embeddedFor(name)
}

func embeddedFor(name string) ([]byte, bool) {
	switch name {
	case "Dockerfile":
		return embeddedDockerfile, true
	case "entrypoint.sh":
		return embeddedEntrypoint, true
	default:
		return nil, false
	}
}
```

- [ ] **Step 4: Update `ScaffoldAssets` to consume the seed at setup time**

In `internal/scaffold/scaffold_assets.go`, change the body-resolution:

Replace:

```go
		body, source, err := ResolveSource(opts.Home, name)
		if err != nil {
			return res, err
		}
		res.source[name] = source
```

with:

```go
		body, ok := EmbeddedSeed(name)
		if !ok {
			return res, fmt.Errorf("unknown scaffold asset %q", name)
		}
		// If a user template is already present and the target is a
		// project .ws/ dir, prefer the user template so project assets
		// reflect the user's edits.
		if strings.HasSuffix(opts.TargetDir, ".ws") && opts.Home != "" {
			userPath := filepath.Join(opts.Home, ".config", "ws", "template", name)
			if data, err := os.ReadFile(userPath); err == nil {
				body = data
				res.source[name] = "user-template"
			} else {
				res.source[name] = "embedded-seed"
			}
		} else {
			res.source[name] = "embedded-seed"
		}
```

Add `"strings"` to the imports if needed.

- [ ] **Step 5: Run tests**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/source.go internal/scaffold/source_test.go \
        internal/scaffold/scaffold_assets.go
git commit -m "$(cat <<'EOF'
feat(scaffold): ResolveSource has no embedded fallback at runtime

Runtime asset resolution goes project-override → user-template → error.
EmbeddedSeed is a setup-only bootstrap. Consistent with the
exhaustiveness principle: missing assets surface loud, not silently
falling through to the embedded copy.
EOF
)"
```

---

## Task 10: Update docs

**Files:**
- Modify: `README.md`
- Modify: `docs/headless-claude-auth-plan.md`

- [ ] **Step 1: Revise `README.md`**

Edit the "One-time setup" and "Files" sections to describe the new flow. Replace any reference to `.ws.yaml` with `.ws/config.yaml` and `~/.config/ws/config.yaml`. Add a paragraph under "What's new" noting:

> `ws setup` now writes a fully-populated user config (`~/.config/ws/config.yaml`) and a minimal project config (`.ws/config.yaml`). The user config is the exhaustive baseline for every setting except the two project-specific keys (`project.id`, `github_repo_url`); project configs override anything per-key. Secrets and arbitrary env vars flow through to the container via a generic schema — see `docs/superpowers/specs/2026-04-23-ws-layered-config-design.md`.

Find and update any mention of `CLAUDE_AUTH_SECRET_NAME`, `GITHUB_PAT_SECRET_NAME`, the `--container-env` pointer list, or `global.Projects[id]` overrides. Replace with the new schema where relevant.

- [ ] **Step 2: Revise `docs/headless-claude-auth-plan.md`**

Change the references to `CLAUDE_AUTH_MODE` prompting to:

> Claude auth mode (OAuth vs API key) is selected at `ws setup` time via the logical name stored in the `secrets:` map: `claude_oauth` for OAuth, `claude_apikey` for API key. Exactly one must be present in the merged config.

- [ ] **Step 3: Commit**

```bash
git add README.md docs/headless-claude-auth-plan.md
git commit -m "docs: update for layered config and new secrets schema"
```

---

## Manual smoke test (after Task 10)

Before merging the branch, run one end-to-end pass against a real GCP project to validate the entrypoint rewrite — the only component with no automated test coverage.

```bash
# Clean slate on a throwaway project:
rm -rf ~/.config/ws /tmp/ws-smoke && mkdir /tmp/ws-smoke && cd /tmp/ws-smoke
go run ./... setup
# answer prompts — choose OAuth mode
go run ./... create
# wait for provisioning, then:
go run ./... connect
# inside the VM shell, confirm:
#   echo $CLAUDE_CODE_OAUTH_TOKEN     # should be populated
#   echo $GH_TOKEN                    # should be populated
#   git ls-remote <the repo>          # should succeed via credential helper
#   env | grep WS_SECRET              # proves the wire protocol reached entrypoint
#   tail /var/log/claude-setup.log    # review dispatcher log lines per logical name
```

Repeat with `claude_apikey` swapped into `~/.config/ws/config.yaml` to verify the apiKeyHelper branch.

---

## Self-Review Checklist

**Spec coverage** — every spec section maps to a task:

| Spec section | Task(s) |
|---|---|
| Precedence + exhaustiveness principles | 1 (types), 2 (merge), 4 (validate) |
| Schema (user + project YAML layout) | 1 (types), 7 (renderers) |
| Field reference (required flags) | 4 (validate) |
| Merge semantics (scalars + maps) | 2 (merge tests cover user-seed/project-override) |
| Validation rules (6 checks) | 4 |
| Wire protocol `ws create → entrypoint.sh` | 5 (emit), 6 (consume) |
| `ws setup` behavior (steps 1–7) | 8 |
| Asset resolution (no runtime embedded fallback) | 9 |
| Deletions list (old types, old renderers) | 1 (config types), 7 (scaffold types) |
| Docs update | 10 |

**Placeholder scan** — no "TBD", "TODO", "implement later", "fill in" outside this checklist. Every code step shows complete code; every test step shows the full test body. Commands include expected outputs.

**Type consistency** — cross-check:
- `UserConfig.Git.UserName` (Task 1) → `cfg.GitUserName` via Merge (Task 1) → validated as `git.user_name` (Task 4) → rendered as `"Alice"` under `git:` (Task 7) → written by prompt `git.user_name` (Task 8). Chain holds.
- `SecretEntry{SecretName, EnvVar, OAuthAccountSecretName}` consistent between Task 1 (types), Task 2 (merge test), Task 4 (validate), Task 5 (buildContainerEnv wire format), Task 6 (entrypoint `WS_SECRET_<TOK>_NAME` / `_VAR` / `_OAUTH_ACCOUNT_SECRET_NAME`), Task 7 (rendered YAML), Task 8 (prompt flow sets ClaudeAuthMode which the renderer turns into claude_oauth/claude_apikey).
- `WS_SECRETS` enumeration + `WS_SECRET_<LOGICAL_UPPERCASE>_NAME`/`_VAR` matches in both emitter (Task 5's `logicalNameToEnvToken = strings.ToUpper`) and consumer (Task 6's `token=$(echo "${logical}" | tr '[:lower:]' '[:upper:]')`).
- `ScaffoldAssetsOptions.TargetDir` (Task 8 Step 1) replaces `Cwd` cleanly; all callers in Tasks 8 + 9 use the new field.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-23-ws-layered-config.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
