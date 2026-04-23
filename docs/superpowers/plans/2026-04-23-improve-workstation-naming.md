# Improve Workstation Naming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ws create` a pure "new workstation" action (fail fast if the target workstation already exists) and change the default workstation name from `<project_id>-<username>` to `<repo>-<username>`, where `<repo>` is the repository name parsed out of `github_repo_url`.

**Architecture:**

1. **Repo-slug name.** Add a pure helper `scaffold.RepoSlugFromURL(url string) string` that returns the repo name ("remote-agent") from an HTTPS or SSH GitHub URL, or `""` if the URL can't be parsed. Change `config.WorkstationName` to take a repo slug instead of a project ID. `config.Merge` normalizes `cfg.GithubRepoURL`, derives the slug, falls back to `cfg.ProjectID` when the slug is empty (so commands still work before `ws create` validates the URL), and stores the result in `cfg.WorkstationName`.

2. **Fail fast on existing workstation.** In `cmd/create.go`, add a small helper `ensureWorkstationAbsent(r, cfg)` that returns an error if `gcloud workstations describe <name>` succeeds. Call it near the top of `runCreate` — before any APIs are enabled or images built — so users get an immediate, actionable error (`workstation "X" already exists — delete it first with "ws delete X"`). Remove the now-dead "workstation exists → skip create" branch in Step 8; keep the `start`-if-not-running logic since we still want the fresh workstation started.

No migration code: existing workstations created under the old name scheme will simply stop being the default. Per project memory, there is one deployment and one user; they can `ws delete` the old workstation before running the new `ws create`.

**Tech Stack:** Go 1.26, `github.com/spf13/cobra`, `github.com/adamtait/remote-agent/internal/{config,gcloud,scaffold}`.

---

## File Map

- **Modify** `internal/scaffold/validate.go` — add `RepoSlugFromURL(url string) string` alongside the existing `NormalizeGithubRepoURL`.
- **Modify** `internal/scaffold/validate_test.go` — add `TestRepoSlugFromURL` with HTTPS/SSH/malformed cases.
- **Modify** `internal/config/merge.go` — change `WorkstationName` signature from `(projectID, username)` to `(repoSlug, username)`; in `Merge`, compute `repoSlug` via `scaffold.NormalizeGithubRepoURL` + `scaffold.RepoSlugFromURL`, fall back to `cfg.ProjectID`.
- **Modify** `internal/config/merge_test.go` — update `TestWorkstationName_*` tests to pass repo slugs; add a merge-level test that verifies `WorkstationName` is derived from `github_repo_url`.
- **Modify** `cmd/create.go` — add `ensureWorkstationAbsent` helper; call it early in `runCreate`; remove the `if !r.Check(...) { create }` wrapper around `workstations create` (workstation is guaranteed absent at this point, so just call create).
- **Modify** `cmd/create_test.go` — add `TestEnsureWorkstationAbsent_Present_Errors` and `TestEnsureWorkstationAbsent_Absent_NoError` using the existing `testRunner` fake.

Assumes cwd is the worktree root: `/home/user/workspace/.worktrees/improve-workstation-naming`.

---

### Task 1: Add `RepoSlugFromURL` helper in `internal/scaffold`

**Files:**
- Modify: `internal/scaffold/validate.go`
- Modify: `internal/scaffold/validate_test.go`

A pure parser: no network, no fs. Handles HTTPS (`https://github.com/owner/repo[.git]`) and SSH (`git@github.com:owner/repo[.git]`). Returns just the repo component. Returns `""` on non-GitHub or unparseable inputs so callers can fall back.

- [ ] **Step 1: Write the failing test**

Append to `internal/scaffold/validate_test.go`:

```go
func TestRepoSlugFromURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "https with .git", input: "https://github.com/owner/repo.git", want: "repo"},
		{name: "https without .git", input: "https://github.com/owner/repo", want: "repo"},
		{name: "https hyphenated", input: "https://github.com/adamtait/remote-agent.git", want: "remote-agent"},
		{name: "https with trailing slash", input: "https://github.com/owner/repo/", want: "repo"},
		{name: "ssh with .git", input: "git@github.com:owner/repo.git", want: "repo"},
		{name: "ssh without .git", input: "git@github.com:adamtait/remote-agent", want: "remote-agent"},
		{name: "empty", input: "", want: ""},
		{name: "garbage", input: "not-a-url", want: ""},
		{name: "https no repo segment", input: "https://github.com/owner", want: ""},
		{name: "non-github host", input: "https://gitlab.com/owner/repo.git", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaffold.RepoSlugFromURL(tc.input)
			if got != tc.want {
				t.Errorf("RepoSlugFromURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scaffold/ -run TestRepoSlugFromURL -v`

Expected: FAIL with `undefined: scaffold.RepoSlugFromURL`.

- [ ] **Step 3: Implement `RepoSlugFromURL`**

In `internal/scaffold/validate.go`, below the existing `NormalizeGithubRepoURL` function, add:

```go
// repoSlugHTTPSRe matches https://github.com/<owner>/<repo>[.git][/] and captures <repo>.
var repoSlugHTTPSRe = regexp.MustCompile(`^https://github\.com/[^/]+/([^/]+?)(?:\.git)?/?$`)

// repoSlugSSHRe matches git@github.com:<owner>/<repo>[.git] and captures <repo>.
var repoSlugSSHRe = regexp.MustCompile(`^git@github\.com:[^/]+/([^/]+?)(?:\.git)?$`)

// RepoSlugFromURL returns the repository name from a GitHub URL, or "" if the
// URL is empty, malformed, or not a GitHub URL. HTTPS and SSH forms are both
// supported; an optional ".git" suffix and trailing slash are stripped.
func RepoSlugFromURL(url string) string {
	if m := repoSlugHTTPSRe.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	if m := repoSlugSSHRe.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scaffold/ -run TestRepoSlugFromURL -v`

Expected: PASS (10 subtests).

- [ ] **Step 5: Run the full scaffold package tests to confirm nothing regressed**

Run: `go test ./internal/scaffold/...`

Expected: `ok github.com/adamtait/remote-agent/internal/scaffold`.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/validate.go internal/scaffold/validate_test.go
git commit -m "feat(scaffold): add RepoSlugFromURL helper"
```

---

### Task 2: Change `WorkstationName` to use a repo slug; drive it from `github_repo_url` in `Merge`

**Files:**
- Modify: `internal/config/merge.go`
- Modify: `internal/config/merge_test.go`

`WorkstationName` is a leaf helper and stays pure; `Merge` becomes the integration point that picks the slug. Fallback chain: repo slug → project ID → `"ws"`. (Project ID fallback preserves a sensible default for commands run before `github_repo_url` is set in `.ws.yaml`; `"ws"` prevents an empty name in the pathological case where neither is set.)

- [ ] **Step 1: Update the existing `TestWorkstationName_*` tests to the new signature**

In `internal/config/merge_test.go`, replace the `TestWorkstationName_Sanitized` and `TestWorkstationName_TruncatedAt63` functions:

```go
func TestWorkstationName_Sanitized(t *testing.T) {
	cases := []struct {
		repoSlug   string
		username   string
		wantPrefix string
	}{
		{"remote-agent", "adam", "remote-agent-adam"},
		{"My_Repo!", "Adam.Tait", "my-repo-adam-tait"},
		{"repo", "user", "repo-user"},
	}
	for _, tc := range cases {
		got := config.WorkstationName(tc.repoSlug, tc.username)
		if !strings.HasPrefix(got, tc.wantPrefix) {
			t.Errorf("WorkstationName(%q, %q) = %q, want prefix %q", tc.repoSlug, tc.username, got, tc.wantPrefix)
		}
		if len(got) > 63 {
			t.Errorf("WorkstationName result too long: %d chars", len(got))
		}
	}
}

func TestWorkstationName_TruncatedAt63(t *testing.T) {
	long := strings.Repeat("a", 40)
	name := config.WorkstationName(long, long)
	if len(name) > 63 {
		t.Errorf("expected ≤63 chars, got %d", len(name))
	}
}
```

- [ ] **Step 2: Add a merge-level test that `WorkstationName` comes from `github_repo_url`**

In `internal/config/merge_test.go`, add this new test next to the existing workstation-name tests:

```go
func TestMerge_WorkstationNameUsesRepoSlug(t *testing.T) {
	project := &config.ProjectConfig{
		ProjectID:     "my-gcp-project",
		GithubRepoURL: "https://github.com/adamtait/remote-agent.git",
	}
	global := &config.GlobalConfig{}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	// The repo slug, not the project ID, should drive the name.
	if !strings.HasPrefix(cfg.WorkstationName, "remote-agent-") {
		t.Errorf("WorkstationName = %q, want it to start with %q", cfg.WorkstationName, "remote-agent-")
	}
	if strings.HasPrefix(cfg.WorkstationName, "my-gcp-project-") {
		t.Errorf("WorkstationName = %q, should not use project_id when repo URL is set", cfg.WorkstationName)
	}
}

func TestMerge_WorkstationNameFallsBackToProjectID(t *testing.T) {
	project := &config.ProjectConfig{ProjectID: "my-gcp-project"} // no GithubRepoURL
	global := &config.GlobalConfig{}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.WorkstationName, "my-gcp-project-") {
		t.Errorf("WorkstationName = %q, want it to start with project_id %q", cfg.WorkstationName, "my-gcp-project-")
	}
}

func TestMerge_WorkstationNameNormalizesSSHRepoURL(t *testing.T) {
	project := &config.ProjectConfig{
		ProjectID:     "p",
		GithubRepoURL: "git@github.com:adamtait/remote-agent.git",
	}
	cfg, err := config.Merge(project, &config.GlobalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.WorkstationName, "remote-agent-") {
		t.Errorf("WorkstationName = %q, want it derived from SSH URL's repo slug", cfg.WorkstationName)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/config/ -run "TestWorkstationName|TestMerge_WorkstationName" -v`

Expected: FAIL — the existing `TestWorkstationName_*` tests fail because `WorkstationName` still takes a project ID (they'll pass by accident only if the first arg happens to look right after sanitization), and the new `TestMerge_WorkstationName*` tests fail because the name is still `<project_id>-<username>`.

- [ ] **Step 4: Update `WorkstationName` signature and `Merge` to use the repo slug**

In `internal/config/merge.go`:

Change the imports block to include the scaffold package:

```go
import (
	"os"
	"os/user"
	"regexp"
	"strings"

	"github.com/adamtait/remote-agent/internal/scaffold"
	"gopkg.in/yaml.v3"
)
```

Replace the block that currently reads (around lines 108–120):

```go
	// Derive username for workstation name; os/user.Current is more reliable than $USER.
	var username string
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	} else {
		username = os.Getenv("USER")
	}
	if username == "" {
		username = "user"
	}
	cfg.WorkstationName = WorkstationName(cfg.ProjectID, username)

	return cfg, nil
}
```

with:

```go
	// Derive username for workstation name; os/user.Current is more reliable than $USER.
	var username string
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	} else {
		username = os.Getenv("USER")
	}
	if username == "" {
		username = "user"
	}

	// Workstation name is derived from the repo slug so a single GCP project
	// can host distinguishable workstations for multiple repos. Fall back to
	// the project ID if the repo URL is unset or unparseable (e.g. when a
	// command runs before .ws.yaml has been populated).
	slug := scaffold.RepoSlugFromURL(scaffold.NormalizeGithubRepoURL(cfg.GithubRepoURL))
	if slug == "" {
		slug = cfg.ProjectID
	}
	if slug == "" {
		slug = "ws"
	}
	cfg.WorkstationName = WorkstationName(slug, username)

	return cfg, nil
}
```

Replace the `WorkstationName` function (around lines 125–140) with:

```go
var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9]+`)

// WorkstationName returns the default workstation name for a repo/user pair.
// Input is sanitized to lowercase alphanumerics and hyphens and truncated to
// 63 characters (GCP Workstation name limit). Exported so it can be tested
// directly.
func WorkstationName(repoSlug, username string) string {
	clean := func(s string) string {
		s = strings.ToLower(s)
		s = nonAlphanumHyphen.ReplaceAllString(s, "-")
		s = strings.Trim(s, "-")
		return s
	}
	name := clean(repoSlug) + "-" + clean(username)
	if len(name) > 63 {
		name = name[:63]
	}
	name = strings.TrimRight(name, "-")
	return name
}
```

- [ ] **Step 5: Update the `WorkstationName` doc comment on the `Config` struct**

In `internal/config/types.go`, change the comment on line 56:

```go
	WorkstationName     string // <project_id>-<username>, sanitized, ≤63 chars
```

to:

```go
	WorkstationName     string // <repo-slug>-<username>, sanitized, ≤63 chars
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/config/ -run "TestWorkstationName|TestMerge" -v`

Expected: PASS for all `TestWorkstationName_*` and `TestMerge_*` tests (including the three new `TestMerge_WorkstationName*` tests).

- [ ] **Step 7: Run the full test suite to catch downstream regressions**

Run: `go test ./...`

Expected: all packages pass. Note: `cmd/connect_test.go` and `cmd/resolve_test.go` construct `config.Config` literals with `WorkstationName: "p-user"` — those remain valid because the Config struct field is unchanged. Only the function signature of `WorkstationName` changed, and those tests don't call it.

- [ ] **Step 8: Commit**

```bash
git add internal/config/merge.go internal/config/types.go internal/config/merge_test.go
git commit -m "feat(config): derive workstation name from repo slug"
```

---

### Task 3: Fail fast in `ws create` when the workstation already exists

**Files:**
- Modify: `cmd/create.go`
- Modify: `cmd/create_test.go`

Extract a tiny helper `ensureWorkstationAbsent(r, cfg)` so we can unit-test the check without running the full create flow. Call it near the top of `runCreate` (after config validation, before `services enable`) so users don't wait on API enables or an image build just to be told the workstation already exists. Delete the existence-check branch around Step 8's `workstations create` — at that point we've proven the workstation does not exist.

- [ ] **Step 1: Write the failing tests for `ensureWorkstationAbsent`**

Append to `cmd/create_test.go`:

```go
func TestEnsureWorkstationAbsent_Absent_NoError(t *testing.T) {
	runner := &testRunner{checks: map[string]bool{}}
	cfg := &config.Config{
		ProjectID:       "p",
		Region:          "r",
		ClusterName:     "cl",
		ConfigName:      "cfg",
		WorkstationName: "remote-agent-user",
	}
	if err := ensureWorkstationAbsent(runner, cfg); err != nil {
		t.Fatalf("expected no error when workstation is absent, got: %v", err)
	}
}

func TestEnsureWorkstationAbsent_Present_Errors(t *testing.T) {
	key := "workstations describe remote-agent-user --cluster cl --config cfg --region r --project p"
	runner := &testRunner{checks: map[string]bool{key: true}}
	cfg := &config.Config{
		ProjectID:       "p",
		Region:          "r",
		ClusterName:     "cl",
		ConfigName:      "cfg",
		WorkstationName: "remote-agent-user",
	}
	err := ensureWorkstationAbsent(runner, cfg)
	if err == nil {
		t.Fatal("expected error when workstation already exists")
	}
	if !strings.Contains(err.Error(), "remote-agent-user") {
		t.Errorf("error should name the workstation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ws delete") {
		t.Errorf("error should suggest `ws delete`, got: %v", err)
	}
}
```

Also add the `config` import at the top of `cmd/create_test.go` if not present. The current file's imports are:

```go
import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)
```

Change to:

```go
import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/ -run TestEnsureWorkstationAbsent -v`

Expected: FAIL — `undefined: ensureWorkstationAbsent`.

- [ ] **Step 3: Implement `ensureWorkstationAbsent` and wire it into `runCreate`**

In `cmd/create.go`, add this helper after `runCreate` (above `ensureClaudeSecret` is fine):

```go
// ensureWorkstationAbsent returns an error if a workstation with cfg.WorkstationName
// already exists. `ws create` is for *creating* new workstations; updating an
// existing one is out of scope. Users delete the existing workstation first.
func ensureWorkstationAbsent(r gcloud.Runner, cfg *config.Config) error {
	if r.Check("workstations", "describe", cfg.WorkstationName,
		"--cluster", cfg.ClusterName, "--config", cfg.ConfigName,
		"--region", cfg.Region, "--project", cfg.ProjectID) {
		return fmt.Errorf("workstation %q already exists — delete it first with `ws delete %s`",
			cfg.WorkstationName, cfg.WorkstationName)
	}
	return nil
}
```

Add the `config` import at the top of `cmd/create.go`. The file's imports currently include:

```go
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/scaffold"
```

Add `"github.com/adamtait/remote-agent/internal/config"` to that block.

Now call `ensureWorkstationAbsent` early in `runCreate`. Find the existing block (around lines 91–93):

```go
	}

	r := runner

	imageURI := fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s",
```

and replace with:

```go
	}

	r := runner

	// `ws create` is for creating new workstations. If one with this name
	// already exists, bail out before enabling APIs or building images.
	if err := ensureWorkstationAbsent(r, cfg); err != nil {
		return err
	}

	imageURI := fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s",
```

- [ ] **Step 4: Run the new tests to verify they pass**

Run: `go test ./cmd/ -run TestEnsureWorkstationAbsent -v`

Expected: PASS (both subtests).

- [ ] **Step 5: Remove the now-dead existence-check branch around `workstations create`**

In `cmd/create.go`, find the block at Step 8 (currently around lines 285–295):

```go
	// Step 8 — Create and start workstation
	logStep(fmt.Sprintf("Ensuring workstation '%s'...", cfg.WorkstationName))
	if !r.Check("workstations", "describe", cfg.WorkstationName,
		"--cluster", cfg.ClusterName, "--config", cfg.ConfigName,
		"--region", cfg.Region, "--project", cfg.ProjectID) {
		if err := r.Run("workstations", "create", cfg.WorkstationName,
			"--cluster", cfg.ClusterName, "--config", cfg.ConfigName,
			"--region", cfg.Region, "--project", cfg.ProjectID); err != nil {
			return fmt.Errorf("creating workstation: %w", err)
		}
	}
```

Replace with:

```go
	// Step 8 — Create and start workstation. `ensureWorkstationAbsent` already
	// guaranteed it doesn't exist, so just create it.
	logStep(fmt.Sprintf("Creating workstation '%s'...", cfg.WorkstationName))
	if err := r.Run("workstations", "create", cfg.WorkstationName,
		"--cluster", cfg.ClusterName, "--config", cfg.ConfigName,
		"--region", cfg.Region, "--project", cfg.ProjectID); err != nil {
		return fmt.Errorf("creating workstation: %w", err)
	}
```

- [ ] **Step 6: Update the command `Long` description to drop the "idempotent" wording**

In `cmd/create.go`, change the `createCmd.Long` field (around lines 24–25):

```go
	Long: `Idempotent: safe to re-run. Each step checks for existence first.
Requires .ws.yaml in the project root with project_id and github_repo_url set.`,
```

to:

```go
	Long: `Create a new GCP Workstation for this repo. Errors if a workstation
with the derived name already exists; delete it first with 'ws delete'.
Requires .ws.yaml in the project root with project_id and github_repo_url set.`,
```

- [ ] **Step 7: Run the full test suite**

Run: `go test ./...`

Expected: all packages pass.

- [ ] **Step 8: Build to catch any type issues**

Run: `go build ./...`

Expected: exits 0 with no output.

- [ ] **Step 9: Commit**

```bash
git add cmd/create.go cmd/create_test.go
git commit -m "feat(create): fail fast when workstation already exists"
```

---

## Self-Review Checklist

- **Spec coverage:**
  - Requirement 1 ("ws create assumes new workstation") → Task 3 (fail-fast + dead-branch removal + updated `Long` description).
  - Requirement 2 ("workstation name includes repo name") → Tasks 1 (slug parser) and 2 (wiring into `Merge`, signature change).
- **Placeholder scan:** none.
- **Type consistency:**
  - `WorkstationName` signature is `(repoSlug, username string) string` in both the tests (Task 2 Step 1) and the implementation (Task 2 Step 4).
  - `ensureWorkstationAbsent(r gcloud.Runner, cfg *config.Config) error` signature matches between test (Task 3 Step 1) and implementation (Task 3 Step 3).
  - `RepoSlugFromURL(url string) string` returns `""` on failure, which `Merge` uses to fall back to `cfg.ProjectID`.

## Execution Notes

- This plan is small (3 tasks, one commit each). TDD cycle per task: write test → red → implement → green → commit.
- No public CLI flag changes; no config-file changes. Existing workstations created under the old name scheme will look like "orphans" to `ws connect`/`ws stop` etc., but `resolveWorkstationName`'s single-workstation fallback handles that case today.
