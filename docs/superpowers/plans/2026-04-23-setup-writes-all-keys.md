# `ws setup` Writes All Config Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ws setup` (interactive) emit every config key — required + optional — in both `.ws/config.yaml` and `~/.config/ws/config.yaml`, matching the structure of `ws setup template`. Keys the user filled in are uncommented with the chosen value; keys the user didn't fill in are commented out with defaults (same as the template command).

**Architecture:** Today `ws setup` calls `projectConfigYAML.marshal()` / `globalConfigYAML.marshal()`, which emit only the fields the user filled in (and skip optional keys entirely unless `--advanced` was passed). `ws setup template` calls `RenderProjectTemplate` / `RenderGlobalTemplate`, which emit every key (commented). Unify: extend the renderers to accept a user-values struct; non-empty user values render uncommented, empty ones fall back to the existing commented-default line. Then point `InteractiveScaffold` at the renderers and delete the scratch `marshal()` methods. Both commands end up driven by a single template renderer, so output structure stays in lockstep.

**Tech Stack:** Go 1.22+, existing package `internal/scaffold`. No new dependencies.

---

## File Structure

### Modified files

| File | What changes |
|---|---|
| `internal/scaffold/templates.go` | Add `UserProjectValues`, `UserGlobalValues`. Extend `RenderProjectTemplate`/`RenderGlobalTemplate` to take a user-values struct — non-empty fields render uncommented; empty fields keep the existing commented default. |
| `internal/scaffold/templates_test.go` | Update existing tests to pass zero-value user structs. Add tests that confirm non-empty user values render uncommented and empty ones stay commented. |
| `internal/scaffold/scaffold.go` | `InteractiveScaffold` builds `UserProjectValues`/`UserGlobalValues` from prompt results and calls the renderers for both the preview and the on-disk write. Delete unused `projectConfigYAML`, `globalConfigYAML`, and their `marshal()` methods. `TemplateScaffold` passes zero-value user structs. |
| `internal/scaffold/scaffold_test.go` | Add assertions that the written interactive files include the commented optional keys (`# cluster_name:`, `# auto_sleep_hours:`, `# projects:`, etc.). Existing `Contains` assertions for user-set keys continue to pass. |

No new files. No other packages touched — YAML load path is comment-agnostic.

---

## Task 1: Extend `RenderProjectTemplate` to accept user values

**Files:**
- Modify: `internal/scaffold/templates.go`
- Test: `internal/scaffold/templates_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/scaffold/templates_test.go`:

```go
func TestRenderProjectTemplate_UserValuesRenderUncommented(t *testing.T) {
	pv := scaffold.ProbedValues{ProjectID: "probed-proj"}
	uv := scaffold.UserProjectValues{
		ProjectID:     "chosen-proj",
		GithubRepoURL: "https://github.com/a/b.git",
		Region:        "us-east1",
		ClusterName:   "my-cluster",
		ConfigName:    "my-config",
		MachineType:   "e2-standard-8",
		DiskSizeGB:    "100",
	}
	got := scaffold.RenderProjectTemplate(pv, uv)

	// User-provided values: uncommented.
	for _, want := range []string{
		"project_id: chosen-proj",
		"github_repo_url: https://github.com/a/b.git",
		"region: us-east1",
		"cluster_name: my-cluster",
		"config_name: my-config",
		"machine_type: e2-standard-8",
		"disk_size_gb: 100",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing uncommented %q\ngot:\n%s", want, got)
		}
	}
	// Probed hint still surfaces even when user overrides.
	if !strings.Contains(got, "probed: probed-proj") {
		t.Errorf("probed hint lost:\n%s", got)
	}
}

func TestRenderProjectTemplate_EmptyUserValuesStayCommented(t *testing.T) {
	got := scaffold.RenderProjectTemplate(
		scaffold.ProbedValues{},
		scaffold.UserProjectValues{
			ProjectID:     "chosen-proj",
			GithubRepoURL: "https://github.com/a/b.git",
		},
	)
	for _, want := range []string{
		"# region:",
		"# cluster_name:",
		"# config_name:",
		"# machine_type:",
		"# disk_size_gb:",
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented default %q\ngot:\n%s", want, got)
		}
	}
	// Negative check: no uncommented versions of these keys.
	for _, unwant := range []string{
		"\nregion:",
		"\ncluster_name:",
		"\nmachine_type:",
		"\ndisk_size_gb:",
	} {
		if strings.Contains(got, unwant) {
			t.Errorf("unexpected uncommented key %q\ngot:\n%s", unwant, got)
		}
	}
}
```

Also update every existing `RenderProjectTemplate(pv)` call site in this file to `RenderProjectTemplate(pv, scaffold.UserProjectValues{})`. Affected tests: `TestRenderProjectTemplate_WithProbes`, `TestRenderProjectTemplate_EmptyProbesShowPlaceholder`, `TestRenderProjectTemplate_IncludesAutoSleepAndShutdown`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/...`
Expected: compile errors on `scaffold.UserProjectValues` (undefined) and the single-argument `RenderProjectTemplate` callers.

- [ ] **Step 3: Add the type and extend the renderer**

In `internal/scaffold/templates.go`, replace the entire `RenderProjectTemplate` function and add `UserProjectValues` above it:

```go
// UserProjectValues holds project-config values the user explicitly chose
// (e.g. via interactive prompts). Non-empty fields render uncommented in
// the generated YAML; empty fields fall back to the commented default
// shown by `ws setup template`.
type UserProjectValues struct {
	ProjectID     string
	GithubRepoURL string
	Region        string
	ClusterName   string
	ConfigName    string
	MachineType   string
	DiskSizeGB    string
}

// RenderProjectTemplate returns a commented .ws/config.yaml. Required
// fields (project_id, github_repo_url) are always uncommented. Optional
// fields render uncommented when set in uv, else commented with defaults.
func RenderProjectTemplate(pv ProbedValues, uv UserProjectValues) string {
	projectID := firstNonEmpty(uv.ProjectID, pv.ProjectID, "my-gcp-project")
	repoURL := firstNonEmpty(uv.GithubRepoURL, pv.GithubRepoURL, "https://github.com/my-org/my-repo.git")

	// optional renders "key: val" uncommented when user provided val,
	// else "# <commentedDefault>" (a full line already including the `# `).
	optional := func(userVal, commentedDefault string) string {
		if userVal != "" {
			// Preserve the trailing comment on the commented-default line
			// (e.g. "# region: us-central1             # optional; ...")
			// by splitting on the first `#` boundary that isn't the leading one.
			key, trailingComment := splitCommentedDefault(commentedDefault)
			if trailingComment != "" {
				return fmt.Sprintf("%s: %s  %s", key, userVal, trailingComment)
			}
			return fmt.Sprintf("%s: %s", key, userVal)
		}
		return commentedDefault
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# ws project config (.ws/config.yaml) — committed to git
# Required fields have no default; uncomment and fill them in.
# Optional fields show the default ws will apply if left unset.

project_id: %s        # REQUIRED — probed: %s
github_repo_url: %s   # REQUIRED — probed: %s

%s
%s
%s
%s
%s
# auto_sleep_hours: 2             # hours idle before stopping (0=never sleep; 1–24; GCP default: 2)
# auto_shutdown_hours: 12         # hours running before force-stop (1–168; GCP default: ~12)
`,
		projectID, probedOr(pv.ProjectID),
		repoURL, probedOr(pv.GithubRepoURL),
		optional(uv.Region, "# region: us-central1             # optional; falls back to global default, then us-central1"),
		optional(uv.ClusterName, "# cluster_name: claude-dev-cluster"),
		optional(uv.ConfigName, "# config_name:  claude-dev-config"),
		optional(uv.MachineType, "# machine_type: e2-standard-4"),
		optional(uv.DiskSizeGB, "# disk_size_gb: 50"),
	)
	return b.String()
}

// splitCommentedDefault parses a commented default line of the form
// "# key: val [  # trailing comment]" and returns ("key", "# trailing comment")
// (trailingComment may be empty). The returned key has no leading `# `.
func splitCommentedDefault(line string) (key, trailingComment string) {
	// Strip leading "# " (or "#").
	s := strings.TrimPrefix(line, "# ")
	s = strings.TrimPrefix(s, "#")
	// Split on a `#` preceded by whitespace to find trailing comment.
	if i := strings.Index(s, "  #"); i >= 0 {
		trailingComment = strings.TrimSpace(s[i:])
		s = s[:i]
	}
	// key is everything before the first `:`.
	if c := strings.Index(s, ":"); c >= 0 {
		return strings.TrimSpace(s[:c]), trailingComment
	}
	return strings.TrimSpace(s), trailingComment
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scaffold/...`
Expected: PASS. Both new tests pass; existing ones still pass because their assertions are `Contains` checks against substrings that remain present (e.g., `# region:` is present both before and after, just with possibly a trailing comment suffix).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/templates.go internal/scaffold/templates_test.go
git commit -m "feat(scaffold): RenderProjectTemplate accepts user-set values

Optional fields render uncommented when set, else commented with defaults."
```

---

## Task 2: Extend `RenderGlobalTemplate` to accept user values

**Files:**
- Modify: `internal/scaffold/templates.go`
- Test: `internal/scaffold/templates_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/scaffold/templates_test.go`:

```go
func TestRenderGlobalTemplate_UserValuesRenderUncommented(t *testing.T) {
	uv := scaffold.UserGlobalValues{
		Region:       "us-east1",
		GitUserName:  "Alice Example",
		GitUserEmail: "alice@example.com",
	}
	got := scaffold.RenderGlobalTemplate(scaffold.ProbedValues{}, uv)

	for _, want := range []string{
		"  region: us-east1",
		`  git_user_name:  "Alice Example"`,
		`  git_user_email: "alice@example.com"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing uncommented %q\ngot:\n%s", want, got)
		}
	}
	// Optional-but-not-prompted keys stay commented.
	for _, want := range []string{
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
		"# projects:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented %q\ngot:\n%s", want, got)
		}
	}
}

func TestRenderGlobalTemplate_EmptyUserValuesStayCommented(t *testing.T) {
	got := scaffold.RenderGlobalTemplate(
		scaffold.ProbedValues{Region: "us-central1"},
		scaffold.UserGlobalValues{},
	)
	for _, want := range []string{
		"# region: us-central1",
		"# git_user_name:",
		"# git_user_email:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented %q\ngot:\n%s", want, got)
		}
	}
	// Negative: no uncommented two-space-indented versions.
	for _, unwant := range []string{
		"\n  region:",
		"\n  git_user_name:",
		"\n  git_user_email:",
	} {
		if strings.Contains(got, unwant) {
			t.Errorf("unexpected uncommented key %q\ngot:\n%s", unwant, got)
		}
	}
}
```

Also update every existing `RenderGlobalTemplate(pv)` call in this file to `RenderGlobalTemplate(pv, scaffold.UserGlobalValues{})`. Affected tests: `TestRenderGlobalTemplate_WithProbes`, `TestRenderGlobalTemplate_NoProbesStillRenders`, `TestRenderGlobalTemplate_EmptyProbesDoNotMangleEnDash`, `TestRenderGlobalTemplate_QuotesGitIdentity`, `TestRenderGlobalTemplate_IncludesAutoSleepAndShutdown`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/...`
Expected: compile errors on `scaffold.UserGlobalValues` (undefined) and the single-argument `RenderGlobalTemplate` callers.

- [ ] **Step 3: Add the type and extend the renderer**

In `internal/scaffold/templates.go`, add `UserGlobalValues` above `RenderGlobalTemplate` and replace the function body:

```go
// UserGlobalValues holds global-config values the user explicitly chose.
// Non-empty fields render uncommented under `defaults:` in the generated
// YAML; empty fields stay commented.
type UserGlobalValues struct {
	Region       string
	GitUserName  string
	GitUserEmail string
}

// RenderGlobalTemplate returns a commented ~/.config/ws/config.yaml with
// defaults and projects sections. Fields set in uv render uncommented.
func RenderGlobalTemplate(pv ProbedValues, uv UserGlobalValues) string {
	regionDefault := firstNonEmpty(pv.Region, "us-central1")
	gitName := quoteOrPlaceholder(pv.GitUserName)
	gitEmail := quoteOrPlaceholder(pv.GitUserEmail)

	// For git_user_name / git_user_email: if the user provided a value,
	// quote it and render uncommented. Otherwise keep the probed-or-
	// placeholder form, commented.
	renderRegion := "# region: " + regionDefault
	if uv.Region != "" {
		renderRegion = "region: " + uv.Region
	}
	renderGitName := "# git_user_name:  " + gitName
	if uv.GitUserName != "" {
		renderGitName = fmt.Sprintf("git_user_name:  %q", uv.GitUserName)
	}
	renderGitEmail := "# git_user_email: " + gitEmail
	if uv.GitUserEmail != "" {
		renderGitEmail = fmt.Sprintf("git_user_email: %q", uv.GitUserEmail)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# ws global config — user-wide defaults and per-project overrides
# All fields optional; uncomment what you want to set.

defaults:
  %s
  %s
  %s
  # auto_sleep_hours: 2           # hours idle before stopping (0=never sleep; 1–24; GCP default: 2)
  # auto_shutdown_hours: 12       # hours running before force-stop (1–168; GCP default: ~12)

# projects:
#   my-gcp-project:
#     service_account_name: claude-workstation-sa
#     image_name:           claude-code-workstation
#     image_tag:            latest
#     repo_name:            claude-workstation-images
#     auto_sleep_hours:   1
#     auto_shutdown_hours: 24
`, renderRegion, renderGitName, renderGitEmail)
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scaffold/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/templates.go internal/scaffold/templates_test.go
git commit -m "feat(scaffold): RenderGlobalTemplate accepts user-set values

defaults.{region,git_user_name,git_user_email} render uncommented when set."
```

---

## Task 3: Drive `InteractiveScaffold` through the template renderers

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Test: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/scaffold/scaffold_test.go`:

```go
func TestInteractiveScaffold_WritesAllKeysInTemplateForm(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-central1",
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

	proj, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	// User-set keys: uncommented.
	for _, want := range []string{
		"project_id: my-proj-123",
		"github_repo_url: https://github.com/a/b.git",
	} {
		if !strings.Contains(string(proj), want) {
			t.Errorf("project config missing %q\ngot:\n%s", want, proj)
		}
	}
	// Optional keys not prompted (no --advanced): commented.
	for _, want := range []string{
		"# region:",
		"# cluster_name:",
		"# config_name:",
		"# machine_type:",
		"# disk_size_gb:",
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
	} {
		if !strings.Contains(string(proj), want) {
			t.Errorf("project config missing commented %q\ngot:\n%s", want, proj)
		}
	}

	global, _ := os.ReadFile(filepath.Join(home, ".config", "ws", "config.yaml"))
	// User-set keys under defaults:
	for _, want := range []string{
		"  region: us-central1",
		`  git_user_name:  "Alice"`,
		`  git_user_email: "alice@example.com"`,
	} {
		if !strings.Contains(string(global), want) {
			t.Errorf("global config missing %q\ngot:\n%s", want, global)
		}
	}
	// Not-prompted keys: commented.
	for _, want := range []string{
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
		"# projects:",
	} {
		if !strings.Contains(string(global), want) {
			t.Errorf("global config missing commented %q\ngot:\n%s", want, global)
		}
	}
}

func TestInteractiveScaffold_AdvancedUncommentsOptionalProjectKeys(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// --advanced: project_id, github_repo_url, region, cluster_name,
	// config_name, machine_type, disk_size_gb, then global region,
	// git_user_name, git_user_email, then confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-east1",
		"my-cluster",
		"my-config",
		"e2-standard-8",
		"100",
		"us-east1",
		"Alice",
		"alice@example.com",
		"y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{}, Advanced: true,
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	proj, _ := os.ReadFile(filepath.Join(cwd, ".ws", "config.yaml"))
	for _, want := range []string{
		"region: us-east1",
		"cluster_name: my-cluster",
		"config_name: my-config",
		"machine_type: e2-standard-8",
		"disk_size_gb: 100",
	} {
		if !strings.Contains(string(proj), want) {
			t.Errorf("project config missing uncommented %q\ngot:\n%s", want, proj)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/...`
Expected: FAIL — the current interactive setup writes only the prompted keys, so the `# cluster_name:` / `# projects:` / etc. `Contains` assertions fail.

- [ ] **Step 3: Rewrite `InteractiveScaffold` to use the template renderers**

In `internal/scaffold/scaffold.go`:

(a) Replace the block that constructs `project`/`global` scratch structs and the block that writes them. Change the declarations (lines ~88–89) from:

```go
project := projectConfigYAML{}
global := globalConfigYAML{}
```

to:

```go
var projectUV UserProjectValues
var globalUV UserGlobalValues
```

(b) Replace every `project.X = ...` assignment in the `writeProject` block with `projectUV.X = ...` — same for the `writeGlobal` block with `globalUV`.

(c) Replace the preview + write blocks:

```go
if writeProject || writeGlobal {
    fmt.Fprintln(opts.Stdout)
    if writeProject {
        fmt.Fprintln(opts.Stdout, "About to write "+projectPath+":")
        fmt.Fprintln(opts.Stdout, indent(RenderProjectTemplate(pv, projectUV), "  "))
    }
    if writeGlobal {
        fmt.Fprintln(opts.Stdout, "About to write "+globalPath+":")
        fmt.Fprintln(opts.Stdout, indent(RenderGlobalTemplate(pv, globalUV), "  "))
    }

    confirm, err := Prompt(stdin, opts.Stdout, "Proceed? [Y/n]", "Y", nil)
    if err != nil {
        return err
    }
    answer := strings.ToLower(strings.TrimSpace(confirm))
    if answer == "n" || answer == "no" {
        return fmt.Errorf("%w: user declined", ErrCancelled)
    }
}
```

and further below:

```go
if writeProject {
    if err := WriteAtomic(projectPath, []byte(RenderProjectTemplate(pv, projectUV)), opts.Force); err != nil {
        return err
    }
}
if writeGlobal {
    if err := WriteAtomic(globalPath, []byte(RenderGlobalTemplate(pv, globalUV)), opts.Force); err != nil {
        return err
    }
}
```

(d) Delete the now-unused types and methods at the bottom of the file:

```go
type projectConfigYAML struct { ... }
func (p projectConfigYAML) marshal() string { ... }
type globalConfigYAML struct { ... }
func (g globalConfigYAML) marshal() string { ... }
```

(e) Update `TemplateScaffold` call sites to pass zero-value user structs:

```go
if err := WriteAtomic(path, []byte(RenderProjectTemplate(pv, UserProjectValues{})), opts.Force); err != nil {
```

```go
if err := WriteAtomic(path, []byte(RenderGlobalTemplate(pv, UserGlobalValues{})), opts.Force); err != nil {
```

- [ ] **Step 4: Run the full package tests**

Run: `go test ./internal/scaffold/...`
Expected: PASS. The two new tests pass. Every pre-existing test passes — they used `Contains` against user-set substrings that are still emitted (now alongside additional commented lines).

- [ ] **Step 5: Run the whole module's tests**

Run: `go test ./...`
Expected: PASS. No other package cares about the on-disk template layout (config loaders ignore comments).

- [ ] **Step 6: Manual smoke check (no commit yet)**

Run: `go build ./... && ./ws setup --help`
Expected: no build errors; help text unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go
git commit -m "feat(setup): write all config keys in template form

ws setup now emits every key in .ws/config.yaml and ~/.config/ws/config.yaml,
matching ws setup template. User-set values are uncommented; unset optional
keys are commented with defaults. Removes the scratch marshal() methods."
```

---

## Self-Review Checklist

Run through this before handing off:

- [ ] **Spec coverage:** The spec is "ws setup writes all config keys in the template, always, for both project and global." Task 3's tests assert on both `.ws/config.yaml` (every project key, commented-or-uncommented) and `~/.config/ws/config.yaml` (every global key plus the `# projects:` block). Covered.
- [ ] **No placeholders:** Grep the plan for `TODO`, `TBD`, `fill in`, `etc` — none outside of Go comments that quote user strings. Every code step shows complete code.
- [ ] **Type consistency:** `UserProjectValues` fields used in Task 1 (`ProjectID`, `GithubRepoURL`, `Region`, `ClusterName`, `ConfigName`, `MachineType`, `DiskSizeGB`) match what Task 3 assigns (`projectUV.ProjectID = projectID`, etc.). `UserGlobalValues` fields (`Region`, `GitUserName`, `GitUserEmail`) match Task 3's `globalUV` assignments. Auto-sleep / auto-shutdown are intentionally absent from both user structs — interactive setup never prompts for them, so they always render as the commented template default.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-23-setup-writes-all-keys.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
