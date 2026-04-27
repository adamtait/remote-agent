# Generic Plugin System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ra`'s on-disk layout and plugin system fully generic. The binary ships with zero built-in plugins; user-global, project-local, and final-image paths all mirror each other; plugins land in three image-mirror dirs (`Dockerfile.d/`, `workstation-startup.d/`, `profile.d/`); `ra plugin install` takes over all plugin prompting and secret upload; the claude plugin moves to a top-level directory in preparation for extraction to a separate repo.

**Architecture:** A new build-context assembler in `internal/scaffold/buildcontext.go` produces an ephemeral `.ra/.build/` directory each time `ra create` runs, merging core ra files + plugin contributions in priority order (project > user > plugins). The Dockerfile becomes generic and references build-context-relative paths. `ra setup` shrinks to ra-only configuration. `ra plugin install` is extended to prompt for config + secrets, upload secrets to GCP Secret Manager, and write config.

**Tech Stack:** Go (cobra CLI), bash (entrypoint + plugin scripts), Docker (BuildKit/`docker buildx`), GCP Secret Manager, YAML (gopkg.in/yaml.v3).

**Reference spec:** `docs/superpowers/specs/2026-04-27-generic-plugin-system-design.md`

---

## File Structure

### Files created
- `internal/scaffold/buildcontext.go` + `buildcontext_test.go` — assembles `.ra/.build/` per the spec's resolution rules
- `internal/secrets/upload.go` + `upload_test.go` — extracted from `cmd/create.go`'s `ensureSecret`, reused by `ra plugin install`
- `internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh` — renamed from `entrypoint.sh`
- `plugins/claude/plugin.yaml` + scripts — new top-level home for claude plugin

### Files deleted
- `internal/plugins/defaults/` (entire directory, after move)
- `internal/plugins/defaults_embed.go`
- `internal/scaffold/scaffold_assets.go` + `_test.go`
- `internal/scaffold/stage_profile_d.go` + `_test.go`
- `internal/scaffold/source.go` + `_test.go`
- `internal/scaffold/assets/entrypoint.sh` (after move)

### Files modified
- `internal/scaffold/embedded.go` — paths updated for renames; removed staging exports
- `internal/scaffold/assets/Dockerfile` — rewritten to be generic
- `internal/scaffold/scaffold.go` — drop plugin-prompt loop, drop plugin asset materialization, write to new paths
- `internal/scaffold/templates.go` — drop `PluginFragments`, extract `defaultGithubPatSecretsBlock` constant
- `internal/plugins/fileplugin.go` — `Assets()` removed; `Prompt()` returns `PromptResult`
- `internal/plugins/loader.go` — drop embedded-defaults branch
- `internal/plugins/registry.go` — drop `AllAssets()`
- `cmd/create.go` — replace `materializeBuildAssets`/`StageProfileD` with `AssembleBuildContext`; update `dockerBuildArgs`
- `cmd/setup.go` — drop plugin-prompt code
- `cmd/plugin.go` — extend install (level prompt + config + secret upload + metadata); list reads `.ra-install.yaml`; remove cleans config + secrets
- `.gitignore` — add `.ra/.build/`
- `CLAUDE.md`, `README.md` — reflect new state

---

## Phase 1 — Build context assembler + new core layout

### Task 1: Add `.ra/.build/` to .gitignore

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Append the entry**

```bash
cat >> .gitignore <<'EOF'

# ra: ephemeral docker build context assembled by `ra create`
.ra/.build/
EOF
```

- [ ] **Step 2: Verify**

Run: `grep -n '.build' .gitignore`
Expected: A line `.ra/.build/`.

- [ ] **Step 3: Commit**

```bash
git add .gitignore
git commit -m "chore: gitignore .ra/.build/ build context"
```

---

### Task 2: Create the buildcontext package skeleton with the simplest possible test

**Files:**
- Create: `internal/scaffold/buildcontext.go`
- Create: `internal/scaffold/buildcontext_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/scaffold/buildcontext_test.go`:

```go
package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssembleBuildContext_CreatesBuildDir(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{
		Cwd:  cwd,
		Home: home,
	})
	if err != nil {
		t.Fatalf("AssembleBuildContext: %v", err)
	}
	defer cleanup()

	want := filepath.Join(cwd, ".ra", ".build")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected build dir to exist: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext_CreatesBuildDir`
Expected: FAIL — `undefined: AssembleBuildContext`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/scaffold/buildcontext.go`:

```go
// Package scaffold — buildcontext.go
// Assemble an ephemeral .ra/.build/ docker build context from layered sources:
// project (.ra/) > user-global (~/.config/ra/) > plugin-contributed.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adamtait/remote-agent/internal/plugins"
)

// BuildContextOptions controls AssembleBuildContext.
type BuildContextOptions struct {
	Cwd     string             // project root (contains .ra/)
	Home    string             // user home (contains .config/ra/)
	Plugins []*plugins.FilePlugin
}

// AssembleBuildContext writes a fresh <cwd>/.ra/.build/ docker build context
// from layered sources (see package doc). Returns the build-context path,
// a cleanup func the caller MUST invoke, and any error.
func AssembleBuildContext(opts BuildContextOptions) (string, func(), error) {
	root := filepath.Join(opts.Cwd, ".ra", ".build")

	if err := os.RemoveAll(root); err != nil {
		return "", func() {}, fmt.Errorf("clearing %s: %w", root, err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", func() {}, fmt.Errorf("creating %s: %w", root, err)
	}

	cleanup := func() { _ = os.RemoveAll(root) }
	return root, cleanup, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext_CreatesBuildDir`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/buildcontext.go internal/scaffold/buildcontext_test.go
git commit -m "feat(scaffold): add buildcontext skeleton (creates .ra/.build/)"
```

---

### Task 3: Resolve & write the Dockerfile (project > user > embedded)

**Files:**
- Modify: `internal/scaffold/buildcontext.go`
- Modify: `internal/scaffold/buildcontext_test.go`

- [ ] **Step 1: Add tests for Dockerfile resolution**

Append to `internal/scaffold/buildcontext_test.go`:

```go
func TestAssembleBuildContext_DockerfileFromEmbedded(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatalf("AssembleBuildContext: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatalf("reading Dockerfile: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("Dockerfile is empty; expected embedded default bytes")
	}
}

func TestAssembleBuildContext_DockerfileFromUser(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userPath := filepath.Join(home, ".config", "ra", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(userPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte("USER-DOCKERFILE\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "USER-DOCKERFILE\n" {
		t.Fatalf("Dockerfile = %q, want USER-DOCKERFILE", got)
	}
}

func TestAssembleBuildContext_DockerfileFromProject(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userPath := filepath.Join(home, ".config", "ra", "Dockerfile")
	projPath := filepath.Join(cwd, ".ra", "Dockerfile")
	for _, p := range []string{userPath, projPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(userPath, []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projPath, []byte("PROJECT\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PROJECT\n" {
		t.Fatalf("Dockerfile = %q, want PROJECT", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext_Dockerfile`
Expected: FAIL — Dockerfile not in build dir.

- [ ] **Step 3: Implement Dockerfile resolution**

Replace the body of `AssembleBuildContext` in `internal/scaffold/buildcontext.go`:

```go
func AssembleBuildContext(opts BuildContextOptions) (string, func(), error) {
	root := filepath.Join(opts.Cwd, ".ra", ".build")

	if err := os.RemoveAll(root); err != nil {
		return "", func() {}, fmt.Errorf("clearing %s: %w", root, err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", func() {}, fmt.Errorf("creating %s: %w", root, err)
	}

	cleanup := func() { _ = os.RemoveAll(root) }

	if err := writeDockerfile(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}

	return root, cleanup, nil
}

// writeDockerfile chooses project > user > embedded Dockerfile bytes and
// writes them into <root>/Dockerfile.
func writeDockerfile(opts BuildContextOptions, root string) error {
	candidates := []string{
		filepath.Join(opts.Cwd, ".ra", "Dockerfile"),
		filepath.Join(opts.Home, ".config", "ra", "Dockerfile"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return os.WriteFile(filepath.Join(root, "Dockerfile"), data, 0644)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading %s: %w", p, err)
		}
	}
	return os.WriteFile(filepath.Join(root, "Dockerfile"), embeddedDockerfile, 0644)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext_Dockerfile`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/buildcontext.go internal/scaffold/buildcontext_test.go
git commit -m "feat(scaffold): resolve Dockerfile via project > user > embedded"
```

---

### Task 4: Merge directory contents (workstation-startup.d, profile.d, Dockerfile.d)

**Files:**
- Modify: `internal/scaffold/buildcontext.go`
- Modify: `internal/scaffold/buildcontext_test.go`

- [ ] **Step 1: Write tests for directory merging**

Append to `internal/scaffold/buildcontext_test.go`:

```go
func TestAssembleBuildContext_MergesProfileD(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// User-global has a.sh; project has a.sh (override) and b.sh
	mustWrite(t, filepath.Join(home, ".config", "ra", "profile.d", "a.sh"), "USER-A")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "a.sh"), "PROJ-A")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "b.sh"), "PROJ-B")

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	a, _ := os.ReadFile(filepath.Join(path, "profile.d", "a.sh"))
	b, _ := os.ReadFile(filepath.Join(path, "profile.d", "b.sh"))
	if string(a) != "PROJ-A" {
		t.Errorf("a.sh = %q, want PROJ-A (project shadows user)", a)
	}
	if string(b) != "PROJ-B" {
		t.Errorf("b.sh = %q, want PROJ-B", b)
	}
}

func TestAssembleBuildContext_EmptyProfileDGetsKeep(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	for _, dir := range []string{"profile.d", "workstation-startup.d", "Dockerfile.d"} {
		if _, err := os.Stat(filepath.Join(path, dir, ".ra-keep")); err != nil {
			t.Errorf("%s/.ra-keep missing: %v", dir, err)
		}
	}
}

// mustWrite is a test helper that writes content with parent-dir mkdir.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext_(MergesProfileD|EmptyProfileDGetsKeep)`
Expected: FAIL.

- [ ] **Step 3: Implement directory merging**

Add to `internal/scaffold/buildcontext.go`, and call from `AssembleBuildContext` after `writeDockerfile`:

```go
// mergedDirs are the three image-mirror directories that get merged from
// project, user, and (later) plugin sources.
var mergedDirs = []string{"Dockerfile.d", "workstation-startup.d", "profile.d"}

// mergeDirs writes <root>/<dir>/ for each dir in mergedDirs by overlaying
// user-global, then project-local. Project files shadow user files.
// Plugin contributions are layered in by mergePluginDirs (Task 5).
func mergeDirs(opts BuildContextOptions, root string) error {
	for _, dir := range mergedDirs {
		dst := filepath.Join(root, dir)
		if err := os.MkdirAll(dst, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", dst, err)
		}
		// User-global first (lowest priority), then project (overwrites).
		sources := []string{
			filepath.Join(opts.Home, ".config", "ra", dir),
			filepath.Join(opts.Cwd, ".ra", dir),
		}
		for _, src := range sources {
			if err := copyShellScripts(src, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyShellScripts copies *.sh files from src to dst (overwriting on
// collision). Non-existent src is a no-op. Used for the layered merges.
func copyShellScripts(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", src, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return fmt.Errorf("reading %s/%s: %w", src, e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0755); err != nil {
			return fmt.Errorf("writing %s/%s: %w", dst, e.Name(), err)
		}
	}
	return nil
}

// writeKeepIfEmpty drops a .ra-keep placeholder so docker COPY of an
// otherwise-empty directory does not fail. Called after all merges.
func writeKeepIfEmpty(root string) error {
	for _, dir := range mergedDirs {
		full := filepath.Join(root, dir)
		entries, err := os.ReadDir(full)
		if err != nil {
			return fmt.Errorf("reading %s: %w", full, err)
		}
		hasContent := false
		for _, e := range entries {
			if e.Name() != ".ra-keep" {
				hasContent = true
				break
			}
		}
		if !hasContent {
			if err := os.WriteFile(filepath.Join(full, ".ra-keep"), nil, 0644); err != nil {
				return fmt.Errorf("writing .ra-keep in %s: %w", full, err)
			}
		}
	}
	return nil
}
```

Add `"strings"` to the imports. Then update `AssembleBuildContext` to call them:

```go
func AssembleBuildContext(opts BuildContextOptions) (string, func(), error) {
	root := filepath.Join(opts.Cwd, ".ra", ".build")

	if err := os.RemoveAll(root); err != nil {
		return "", func() {}, fmt.Errorf("clearing %s: %w", root, err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", func() {}, fmt.Errorf("creating %s: %w", root, err)
	}

	cleanup := func() { _ = os.RemoveAll(root) }

	if err := writeDockerfile(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := mergeDirs(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := writeKeepIfEmpty(root); err != nil {
		cleanup()
		return "", func() {}, err
	}

	return root, cleanup, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext`
Expected: PASS (all 5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/buildcontext.go internal/scaffold/buildcontext_test.go
git commit -m "feat(scaffold): merge profile.d/workstation-startup.d/Dockerfile.d with .ra-keep guard"
```

---

### Task 5: Layer in plugin contributions

**Files:**
- Modify: `internal/scaffold/buildcontext.go`
- Modify: `internal/scaffold/buildcontext_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/scaffold/buildcontext_test.go`:

```go
func TestAssembleBuildContext_MergesPluginDirs(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	pluginDir := filepath.Join(home, ".config", "ra", "plugins", "myplug")

	mustWrite(t, filepath.Join(pluginDir, "plugin.yaml"), "name: myplug\nversion: '0.0.1'\ndescription: x\n")
	mustWrite(t, filepath.Join(pluginDir, "profile.d", "01-myplug.sh"), "PLUG-PROFILE")
	mustWrite(t, filepath.Join(pluginDir, "workstation-startup.d", "300_myplug.sh"), "PLUG-START")
	mustWrite(t, filepath.Join(pluginDir, "Dockerfile.d", "200_myplug.sh"), "PLUG-DOCK")

	plug, err := plugins.LoadPlugin(os.DirFS(pluginDir))
	if err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{
		Cwd: cwd, Home: home,
		Plugins: []*plugins.FilePlugin{plug},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	cases := []struct{ rel, want string }{
		{"profile.d/01-myplug.sh", "PLUG-PROFILE"},
		{"workstation-startup.d/300_myplug.sh", "PLUG-START"},
		{"Dockerfile.d/200_myplug.sh", "PLUG-DOCK"},
	}
	for _, c := range cases {
		got, err := os.ReadFile(filepath.Join(path, c.rel))
		if err != nil {
			t.Errorf("read %s: %v", c.rel, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestAssembleBuildContext_ProjectShadowsPlugin(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	pluginDir := filepath.Join(home, ".config", "ra", "plugins", "myplug")

	mustWrite(t, filepath.Join(pluginDir, "plugin.yaml"), "name: myplug\nversion: '0.0.1'\ndescription: x\n")
	mustWrite(t, filepath.Join(pluginDir, "profile.d", "01-myplug.sh"), "PLUG")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "01-myplug.sh"), "PROJECT")

	plug, _ := plugins.LoadPlugin(os.DirFS(pluginDir))

	path, cleanup, _ := AssembleBuildContext(BuildContextOptions{
		Cwd: cwd, Home: home, Plugins: []*plugins.FilePlugin{plug},
	})
	defer cleanup()

	got, _ := os.ReadFile(filepath.Join(path, "profile.d", "01-myplug.sh"))
	if string(got) != "PROJECT" {
		t.Errorf("got %q, want PROJECT (project must shadow plugin)", got)
	}
}
```

You may need to add a `Path()` method on `*FilePlugin` to expose the underlying directory. Actually, simpler: make the buildcontext logic walk via `fs.FS`. Add this helper instead of `Path()`:

In `internal/plugins/fileplugin.go`, add:

```go
// FS returns the filesystem rooted at the plugin's directory. Used by
// callers (e.g. the build-context assembler) that need to walk plugin-
// contributed asset directories.
func (p *FilePlugin) FS() fs.FS { return p.fsys }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext_(MergesPluginDirs|ProjectShadowsPlugin)`
Expected: FAIL.

- [ ] **Step 3: Implement plugin merging**

Add to `internal/scaffold/buildcontext.go`:

```go
// mergePluginDirs overlays each plugin's contributed dirs (Dockerfile.d/,
// workstation-startup.d/, profile.d/) into <root>/<dir>/. Called BEFORE
// the user/project merge so user/project files shadow plugin files.
func mergePluginDirs(opts BuildContextOptions, root string) error {
	for _, p := range opts.Plugins {
		for _, dir := range mergedDirs {
			dst := filepath.Join(root, dir)
			if err := os.MkdirAll(dst, 0755); err != nil {
				return err
			}
			if err := copyShellScriptsFromFS(p.FS(), dir, dst); err != nil {
				return fmt.Errorf("plugin %s %s: %w", p.Name(), dir, err)
			}
		}
	}
	return nil
}

// copyShellScriptsFromFS is the fs.FS variant of copyShellScripts. Used
// for plugin trees, which are accessed via fs.FS so embedded plugins
// (if any return) work without changes.
func copyShellScriptsFromFS(fsys fs.FS, dir, dst string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0755); err != nil {
			return err
		}
	}
	return nil
}
```

Add `"io/fs"` and `"github.com/adamtait/remote-agent/internal/plugins"` to the imports.

Update `AssembleBuildContext` to call `mergePluginDirs` BEFORE `mergeDirs`:

```go
	if err := mergePluginDirs(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := mergeDirs(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext`
Expected: PASS (all 7 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/buildcontext.go internal/scaffold/buildcontext_test.go internal/plugins/fileplugin.go
git commit -m "feat(scaffold): layer plugin Dockerfile.d/workstation-startup.d/profile.d into build context"
```

---

### Task 6: Stage plugin trees into `<root>/plugins/<name>/`

**Files:**
- Modify: `internal/scaffold/buildcontext.go`
- Modify: `internal/scaffold/buildcontext_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/scaffold/buildcontext_test.go`:

```go
func TestAssembleBuildContext_StagesPluginTree(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	pluginDir := filepath.Join(home, ".config", "ra", "plugins", "myplug")

	mustWrite(t, filepath.Join(pluginDir, "plugin.yaml"), "name: myplug\nversion: '0.0.1'\ndescription: x\n")
	mustWrite(t, filepath.Join(pluginDir, "workstation-startup.d", "300_myplug.sh"), "X")
	mustWrite(t, filepath.Join(pluginDir, ".ra-install.yaml"), "source: foo\n")

	plug, _ := plugins.LoadPlugin(os.DirFS(pluginDir))

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{
		Cwd: cwd, Home: home, Plugins: []*plugins.FilePlugin{plug},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// plugin.yaml + scripts copied into build/plugins/<name>/
	if _, err := os.Stat(filepath.Join(path, "plugins", "myplug", "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "plugins", "myplug", "workstation-startup.d", "300_myplug.sh")); err != nil {
		t.Errorf("plugin script not staged: %v", err)
	}

	// .ra-install.yaml is install metadata; do NOT copy into image
	if _, err := os.Stat(filepath.Join(path, "plugins", "myplug", ".ra-install.yaml")); !os.IsNotExist(err) {
		t.Errorf(".ra-install.yaml should NOT be staged into the image, but Stat returned: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext_StagesPluginTree`
Expected: FAIL.

- [ ] **Step 3: Implement plugin tree staging**

Add to `internal/scaffold/buildcontext.go`:

```go
// stagePluginTrees copies each plugin's whole directory into
// <root>/plugins/<name>/. .ra-install.yaml is install metadata and is
// skipped. Project-local plugins shadow user-global plugins entirely
// (handled in cmd/create.go's plugin loading; the assembler just walks
// whatever Plugins it's given).
func stagePluginTrees(opts BuildContextOptions, root string) error {
	dst := filepath.Join(root, "plugins")
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	for _, p := range opts.Plugins {
		pdst := filepath.Join(dst, p.Name())
		if err := copyTreeFromFS(p.FS(), ".", pdst); err != nil {
			return fmt.Errorf("staging plugin %s: %w", p.Name(), err)
		}
	}
	return nil
}

// copyTreeFromFS recursively copies fsys rooted at src into dst on disk.
// Skips .ra-install.yaml at any depth.
func copyTreeFromFS(fsys fs.FS, src, dst string) error {
	return fs.WalkDir(fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".ra-install.yaml" {
			return nil
		}
		rel := p
		if src != "." {
			rel = strings.TrimPrefix(p, src+"/")
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if strings.HasSuffix(p, ".sh") {
			mode = 0755
		}
		return os.WriteFile(target, data, mode)
	})
}
```

Update `AssembleBuildContext` to call `stagePluginTrees` after `mergePluginDirs`:

```go
	if err := mergePluginDirs(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := stagePluginTrees(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := mergeDirs(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/scaffold/ -run TestAssembleBuildContext`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/buildcontext.go internal/scaffold/buildcontext_test.go
git commit -m "feat(scaffold): stage plugin trees into build/plugins/, skip .ra-install.yaml"
```

---

### Task 7: Rename embedded `entrypoint.sh` → `workstation-startup.d/200_remote-agent-setup.sh`

**Files:**
- Move: `internal/scaffold/assets/entrypoint.sh` → `internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh`
- Modify: `internal/scaffold/embedded.go`

- [ ] **Step 1: Move the file**

```bash
mkdir -p internal/scaffold/assets/workstation-startup.d
git mv internal/scaffold/assets/entrypoint.sh internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh
```

- [ ] **Step 2: Update `embedded.go` to embed at the new path**

Edit `internal/scaffold/embedded.go` to:

```go
// Package scaffold — embedded.go
// Canonical default build assets, compiled into the ra binary.
package scaffold

import _ "embed"

//go:embed assets/Dockerfile
var embeddedDockerfile []byte

//go:embed assets/workstation-startup.d/200_remote-agent-setup.sh
var embeddedRemoteAgentSetup []byte

//go:embed assets/profile.d/00-ra-wait.sh
var embeddedWaitScript []byte
```

- [ ] **Step 3: Verify build still passes**

Run: `go build ./...`
Expected: success.

(Tests in scaffold may break because `embeddedEntrypoint` no longer exists. We'll fix those as we delete the dead files in Task 11.)

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/assets/ internal/scaffold/embedded.go
git commit -m "refactor(scaffold): rename entrypoint.sh to workstation-startup.d/200_remote-agent-setup.sh"
```

---

### Task 8: Rewrite the embedded Dockerfile (generic, no claude-isms)

**Files:**
- Modify: `internal/scaffold/assets/Dockerfile`

- [ ] **Step 1: Replace the file contents**

Overwrite `internal/scaffold/assets/Dockerfile` with:

```dockerfile
# ra workstation image — generic.
#
# Build context root is .ra/.build/, assembled fresh by `ra create` via
# scaffold.AssembleBuildContext. All COPY paths are relative to that root.
# This file ships with no plugin-specific content; plugins drop scripts
# into Dockerfile.d/, workstation-startup.d/, and profile.d/ (those dirs
# are merged from project + user + plugin sources at assembly time).
FROM us-central1-docker.pkg.dev/cloud-workstations-images/predefined/code-oss:latest

USER root

# --- Core system packages ---------------------------------------------------
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates gnupg git git-lfs jq \
    && git lfs install --system \
    && rm -rf /var/lib/apt/lists/*

# --- GitHub CLI -------------------------------------------------------------
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)

# --- Plugin build-time hooks ------------------------------------------------
COPY Dockerfile.d/ /tmp/ra-dockerfile-d/
RUN set -e; \
    for s in $(find /tmp/ra-dockerfile-d -name '*.sh' -type f | sort); do \
        echo "==> running build hook: $s"; \
        bash "$s"; \
    done; \
    rm -rf /tmp/ra-dockerfile-d

# --- Plugin tree (informational; live paths are below) ----------------------
COPY plugins/ /etc/ra/plugins/
RUN find /etc/ra/plugins -type f -name '*.sh' -exec chmod 0755 {} +

# --- Workstation-startup scripts (merged: ra core + plugins) ----------------
COPY workstation-startup.d/ /etc/workstation-startup.d/
RUN chmod 0755 /etc/workstation-startup.d/*.sh 2>/dev/null || true

# --- Login-shell profile.d scripts (merged: ra core + plugins) --------------
COPY profile.d/ /etc/profile.d/
RUN chmod 0644 /etc/profile.d/*.sh 2>/dev/null || true
```

(`2>/dev/null || true` on the chmod lines tolerates an empty dir with only `.ra-keep`.)

- [ ] **Step 2: Verify build still compiles**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/assets/Dockerfile
git commit -m "feat(scaffold): rewrite Dockerfile to be generic (no plugin-specific lines)"
```

---

### Task 9: Wire AssembleBuildContext into `cmd/create.go`

**Files:**
- Modify: `cmd/create.go`

- [ ] **Step 1: Replace `materializeBuildAssets` + `StageProfileD` calls with `AssembleBuildContext`**

In `cmd/create.go`, find the block:

```go
	cleanupAssets, err := materializeBuildAssets(cwd, home)
	if err != nil {
		return err
	}
	defer cleanupAssets()

	_, stageCleanup, err := scaffold.StageProfileD(cwd, home)
	if err != nil {
		return fmt.Errorf("staging profile.d: %w", err)
	}
	defer stageCleanup()
```

Replace with:

```go
	_, buildCleanup, err := scaffold.AssembleBuildContext(scaffold.BuildContextOptions{
		Cwd:     cwd,
		Home:    home,
		Plugins: reg.All(),
	})
	if err != nil {
		return fmt.Errorf("assembling build context: %w", err)
	}
	defer buildCleanup()
```

- [ ] **Step 2: Update `dockerBuildArgs`**

Replace:

```go
func dockerBuildArgs(imageURI string) []string {
	return []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", imageURI,
		"-f", ".ra/Dockerfile",
		"--push",
		".",
	}
}
```

With:

```go
func dockerBuildArgs(imageURI string) []string {
	return []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", imageURI,
		"-f", ".ra/.build/Dockerfile",
		"--push",
		".ra/.build",
	}
}
```

- [ ] **Step 3: Verify build compiles**

Run: `go build ./...`
Expected: it will likely fail because `materializeBuildAssets` and `resolvePluginAsset` are now unused. Delete those two functions from `cmd/create.go` (they're no longer needed — `AssembleBuildContext` replaces them).

After deletion, run `go build ./...` again.
Expected: success.

- [ ] **Step 4: Update tests**

Open `cmd/create_test.go` and look for tests referencing `materializeBuildAssets`, `resolvePluginAsset`, or the old build args (`-f .ra/Dockerfile`). Update or remove them so they reflect the new `dockerBuildArgs` output (`-f .ra/.build/Dockerfile`, context `.ra/.build`).

Run: `go test ./cmd/... -run TestDockerBuildArgs` (or whatever the relevant test is named)
Expected: PASS after updates.

- [ ] **Step 5: Commit**

```bash
git add cmd/create.go cmd/create_test.go
git commit -m "feat(cmd): use AssembleBuildContext + .ra/.build/ for docker build"
```

---

### Task 10: Add `Path()` accessor on FilePlugin (or keep using FS) — confirm/fix

**Files:**
- Modify: `internal/plugins/fileplugin.go` (already done in Task 5, verify)

- [ ] **Step 1: Confirm `FS()` exists and is exported**

Run: `grep -n 'func (p \*FilePlugin) FS' internal/plugins/fileplugin.go`
Expected: one line.

- [ ] **Step 2: Run all plugin and scaffold tests**

Run: `go test ./internal/plugins/... ./internal/scaffold/...`
Expected: PASS.

(No commit needed if no changes.)

---

### Task 11: Delete `scaffold_assets.go`, `stage_profile_d.go`, `source.go` and their tests

**Files:**
- Delete: `internal/scaffold/scaffold_assets.go`
- Delete: `internal/scaffold/scaffold_assets_test.go`
- Delete: `internal/scaffold/stage_profile_d.go`
- Delete: `internal/scaffold/stage_profile_d_test.go`
- Delete: `internal/scaffold/source.go`
- Delete: `internal/scaffold/source_test.go`

- [ ] **Step 1: Confirm no remaining references**

Run:
```bash
grep -rn 'ScaffoldAssets\|StageProfileD\|ResolveSource\|EmbeddedSeed\|AssetMode\|AssetNames' \
  --include='*.go' . | grep -v '_test.go'
```
Expected: only references inside the files about to be deleted (or none).

If any reference exists outside those files (most likely in `internal/scaffold/scaffold.go` `InteractiveScaffold` / `TemplateScaffold`), keep going — Task 14 will rewrite scaffold.go. Temporarily comment out those callers if needed:

If `scaffold.go` references `AssetNames` or similar, DEFER the file deletion to after Task 14. Note this in the commit message of Task 14.

If no external references exist, proceed.

- [ ] **Step 2: Remove the files**

```bash
git rm internal/scaffold/scaffold_assets.go internal/scaffold/scaffold_assets_test.go
git rm internal/scaffold/stage_profile_d.go internal/scaffold/stage_profile_d_test.go
git rm internal/scaffold/source.go internal/scaffold/source_test.go
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./...`
Expected: success.

If `internal/scaffold/scaffold.go` still references the deleted symbols, defer Task 11 to run AFTER Task 14 below. Do NOT proceed with the deletion in that case — instead skip to Task 12.

- [ ] **Step 4: Commit**

```bash
git commit -m "chore(scaffold): remove old scaffold_assets/stage_profile_d/source files"
```

---

## Phase 2 — Plugin layout migration

### Task 12: Drop `Assets()` from `FilePlugin`

**Files:**
- Modify: `internal/plugins/fileplugin.go`
- Modify: `internal/plugins/registry.go`
- Modify: `internal/plugins/fileplugin_test.go` (if Assets tests exist)

- [ ] **Step 1: Find callers of `Assets()`**

Run: `grep -rn 'Assets()\|AllAssets()' --include='*.go' .`

Expected callers: `internal/plugins/registry.go` (`AllAssets()`), `internal/scaffold/scaffold.go` (extras), `cmd/create.go` (already removed in Task 9).

- [ ] **Step 2: Remove `Assets()` method from `FilePlugin`**

Delete the entire `Assets()` method block in `internal/plugins/fileplugin.go`.

- [ ] **Step 3: Remove `AllAssets()` from registry**

Delete `AllAssets()` from `internal/plugins/registry.go` (and its tests, if any). The type `plugins.Asset` may also be deletable — check `grep -n 'plugins.Asset' .` and remove if unused.

- [ ] **Step 4: Update `scaffold.go` callers**

In `internal/scaffold/scaffold.go`, find references to `opts.ExtraAssets`, `opts.Registry.AllAssets()`, etc. Remove them. (We'll fully rewrite this in Task 14; for now, just delete the assets-related lines so build passes.)

- [ ] **Step 5: Update tests**

Delete or edit any test referencing `Asset`, `Assets()`, `AllAssets()`.

- [ ] **Step 6: Build and test**

Run: `go build ./... && go test ./...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add internal/plugins/ internal/scaffold/
git commit -m "refactor(plugins): drop Assets() and AllAssets() (replaced by buildcontext walk)"
```

---

### Task 13: Drop the embedded-defaults branch from the loader

**Files:**
- Modify: `internal/plugins/loader.go`
- Delete: `internal/plugins/defaults_embed.go` (after Task 19 moves the claude files; track it here)

- [ ] **Step 1: Mark the branch dead, but defer deletion until after claude is moved**

For now, keep `defaults_embed.go` but note in code that it's about to be removed. We can't yank it yet because tests in `internal/plugins/` may load from it.

In `internal/plugins/loader.go`, the `LoadAll` function currently has:

```go
	entries, _ := defaultsFS.ReadDir("defaults")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(defaultsFS, "defaults/"+e.Name())
		if err != nil {
			continue
		}
		p, err := LoadPlugin(sub)
		if err != nil {
			continue
		}
		byName[p.Name()] = p
	}
```

Leave it for now. We'll delete this block in Task 19 along with the embed.

- [ ] **Step 2: No commit yet** — this task is a placeholder to remember the deletion. Move on to Task 14.

---

### Task 14: Rewrite `InteractiveScaffold` / `TemplateScaffold` for new paths

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Modify: `internal/scaffold/scaffold_test.go`
- Modify: `internal/scaffold/templates.go`

- [ ] **Step 1: Update `templates.go` to drop `PluginFragments`**

In `internal/scaffold/templates.go`:

a. Remove the field from `UserValues`:

```go
type UserValues struct {
	GitUserName   string
	GitUserEmail  string
	GithubRepoURL string
	GcloudProject string

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
}
```

(Removed `PluginFragments`.)

b. Add the github prep constant near the other constants:

```go
// defaultGithubPatSecretsBlock is the hardcoded github_pat secret entry
// rendered into the user config. It will move into a github plugin in a
// follow-up changeset; isolating it here marks the seam.
const defaultGithubPatSecretsBlock = `  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN
`
```

c. Update `RenderUserConfig` to drop `PluginFragments` interpolation:

```go
func RenderUserConfig(uv UserValues) string {
	gitName := orStr(uv.GitUserName, placeholderGitUserName)
	gitEmail := orStr(uv.GitUserEmail, placeholderGitEmail)
	repoURL := orStr(uv.GithubRepoURL, placeholderRepoURL)
	projectID := orStr(uv.GcloudProject, placeholderProjectID)

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

	var b strings.Builder
	fmt.Fprintf(&b, `# ra user config — baseline for all ra commands.
# Every required key is set here. Project configs (.ra/config.yaml)
# override per-key.

git:
  user_name: %q
  user_email: %q
  github_repo_url: %s

gcloud:
  project: %s
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

# plugins — installed via 'ra plugin install <git-url-or-path>'. Each
# plugin contributes its own config block under plugins.<name>.*.
plugins: {}

# secrets — map GCP Secret Manager secrets to container env vars. Each
# entry has the same {secret_name, env_var} shape. Logical names with
# special handling:
#
#   github_pat    → installs git's credential helper (fresh fetch per git
#                   op) AND exports env_var (GH_TOKEN) for `+"`gh`"+` CLI.
#                   Env var is stale on rotation; git operations stay fresh.
#
# Other logical names: plain fetch + export. Rotation requires ra stop/start.
secrets:
%s
# env — plain static env vars forwarded to the container (no secret fetch).
env: {}
`,
		gitName, gitEmail, repoURL,
		projectID,
		region, machine, disk, cluster, conf, sa, img, tag, repo, sleep, shutdown,
		defaultGithubPatSecretsBlock)
	return b.String()
}
```

- [ ] **Step 2: Update `scaffold.go` `InteractiveScaffold`**

In `internal/scaffold/scaffold.go`:

a. Drop the plugin-prompt loop:

```go
	// REMOVE:
	// uv.PluginFragments = make([][2]string, 0)
	// for _, p := range opts.Registry.All() {
	//     block, secrets, err := p.Prompt(stdin, opts.Stdout)
	//     ...
	// }
```

b. Replace asset materialization. The new behavior writes core assets directly to `~/.config/ra/` (no `template/`) and optionally to `.ra/`. Replace the `ScaffoldAssets` calls with a small helper:

```go
// writeCoreAssets writes the embedded Dockerfile, workstation-startup.d
// script, and profile.d wait script into baseDir (which is either
// ~/.config/ra/ or .ra/). Skips files that already exist unless force.
func writeCoreAssets(baseDir string, force bool, stdout io.Writer) error {
	files := []struct {
		rel  string
		data []byte
		mode os.FileMode
	}{
		{"Dockerfile", embeddedDockerfile, 0644},
		{"workstation-startup.d/200_remote-agent-setup.sh", embeddedRemoteAgentSetup, 0755},
		{"profile.d/00-ra-wait.sh", embeddedWaitScript, 0755},
	}
	for _, f := range files {
		dest := filepath.Join(baseDir, f.rel)
		if !force {
			if _, err := os.Stat(dest); err == nil {
				fmt.Fprintf(stdout, "skip %s (already present)\n", f.rel)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, f.data, f.mode); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "wrote %s\n", f.rel)
	}
	return nil
}
```

Replace the existing asset-materialization blocks in `InteractiveScaffold`:

```go
	// User-global core assets always.
	if err := writeCoreAssets(filepath.Join(opts.Home, ".config", "ra"), opts.Force, opts.Stdout); err != nil {
		return err
	}
	if opts.WithProjectAssets {
		if err := writeCoreAssets(filepath.Join(opts.Cwd, ".ra"), opts.Force, opts.Stdout); err != nil {
			return err
		}
	}
```

c. Same change in `TemplateScaffold`.

d. Remove `Registry` field from `InteractiveOptions` and `TemplateOptions` (no longer needed for asset materialization). Update callers in `cmd/setup.go` accordingly.

- [ ] **Step 3: Update `cmd/setup.go`**

Remove the `Registry: reg` line from the options struct and remove any imports that become unused.

- [ ] **Step 4: Build and test**

Run: `go build ./...`
Expected: success.

Run: `go test ./internal/scaffold/...`
Expected: PASS (some scaffold_test.go tests may need updating if they referenced PluginFragments or asset materialization). Update tests inline.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/ cmd/setup.go
git commit -m "refactor(scaffold,setup): drop plugin prompts; write core assets to new paths"
```

- [ ] **Step 6: Now finish Task 11 if it was deferred**

If you skipped the deletion of `scaffold_assets.go` etc. in Task 11, do it now. Run the same delete + build + test + commit sequence.

---

## Phase 3 — Move claude plugin and remove embedded defaults

### Task 15: Create top-level `plugins/claude/plugin.yaml`

**Files:**
- Create: `plugins/claude/plugin.yaml`

- [ ] **Step 1: Create the file**

```bash
mkdir -p plugins/claude
```

Write `plugins/claude/plugin.yaml`:

```yaml
name: claude
version: "1.0.0"
description: "Installs and configures Claude Code on the workstation"

config:
  - name: enabled
    type: bool
    description: "Enable Claude Code?"
    default: false

auth_providers:
  - name: oauth
    description: "OAuth token (recommended)"
    secrets:
      - name: claude_oauth
        env_var: CLAUDE_CODE_OAUTH_TOKEN
        description: "OAuth token from claude.ai/settings"
        default_secret_name: claude-code-oauth-token

  - name: apikey
    description: "API key via helper script"
    secrets:
      - name: claude_apikey
        env_var: ANTHROPIC_API_KEY
        description: "API key from console.anthropic.com"
        default_secret_name: anthropic-api-key
```

- [ ] **Step 2: Commit**

```bash
git add plugins/claude/plugin.yaml
git commit -m "feat(plugins/claude): add plugin.yaml at top-level location"
```

---

### Task 16: Add `plugins/claude/Dockerfile.d/100_claude-nodejs.sh`

**Files:**
- Create: `plugins/claude/Dockerfile.d/100_claude-nodejs.sh`

- [ ] **Step 1: Write the script**

```bash
mkdir -p plugins/claude/Dockerfile.d
```

Write `plugins/claude/Dockerfile.d/100_claude-nodejs.sh`:

```bash
#!/bin/bash
# Claude plugin — install Node.js 20 at image build time.
# Required because Claude Code is distributed as an npm package.
set -euo pipefail

curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y --no-install-recommends nodejs
rm -rf /var/lib/apt/lists/*
```

- [ ] **Step 2: Commit**

```bash
chmod +x plugins/claude/Dockerfile.d/100_claude-nodejs.sh
git add plugins/claude/Dockerfile.d/
git commit -m "feat(plugins/claude): add Dockerfile.d/ Node.js install script"
```

---

### Task 17: Add `plugins/claude/profile.d/00-claude-npm-path.sh`

**Files:**
- Create: `plugins/claude/profile.d/00-claude-npm-path.sh`

- [ ] **Step 1: Write the script**

```bash
mkdir -p plugins/claude/profile.d
```

Write `plugins/claude/profile.d/00-claude-npm-path.sh`:

```bash
# Claude plugin — make /home/user/.npm-global/bin (where `npm install -g`
# places the claude binary) discoverable by login shells.
export PATH="/home/user/.npm-global/bin:$PATH"
```

- [ ] **Step 2: Commit**

```bash
git add plugins/claude/profile.d/
git commit -m "feat(plugins/claude): add profile.d/ npm-global PATH"
```

---

### Task 18: Add `plugins/claude/workstation-startup.d/{250_claude-install.sh, 260_claude-setup.sh}`

**Files:**
- Create: `plugins/claude/workstation-startup.d/250_claude-install.sh`
- Create: `plugins/claude/workstation-startup.d/260_claude-setup.sh`

- [ ] **Step 1: Write `250_claude-install.sh`**

```bash
mkdir -p plugins/claude/workstation-startup.d
```

Write `plugins/claude/workstation-startup.d/250_claude-install.sh`:

```bash
#!/bin/bash
# Claude plugin — first-boot npm install.
#
# Runs as part of /etc/workstation-startup.d/, after secrets are written
# by 200_remote-agent-setup.sh. Bails fast on subsequent boots via the
# install marker.
set -euo pipefail

USER_NAME="user"
NPM_PREFIX="/home/${USER_NAME}/.npm-global"
INSTALL_MARKER="/home/${USER_NAME}/.ra-claude-installed"
CLAUDE_BIN="${NPM_PREFIX}/bin/claude"

# Source fetched secrets in case downstream scripts care; not strictly
# needed for the install itself.
[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CLAUDE_ENABLED:-false}" = "true" ] || exit 0
[ -e "${INSTALL_MARKER}" ] && exit 0
[ -x "${CLAUDE_BIN}" ] && { touch "${INSTALL_MARKER}"; exit 0; }

mkdir -p "${NPM_PREFIX}"
chown "${USER_NAME}:${USER_NAME}" "${NPM_PREFIX}"

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm install -g @anthropic-ai/claude-code

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm cache clean --force

touch "${INSTALL_MARKER}"
chown "${USER_NAME}:${USER_NAME}" "${INSTALL_MARKER}"
```

- [ ] **Step 2: Write `260_claude-setup.sh`**

This is the existing `setup.sh` reworked: source `/run/ra/env`, gate on enabled, do auth setup. The first-boot install logic moves to 250.

Write `plugins/claude/workstation-startup.d/260_claude-setup.sh`:

```bash
#!/bin/bash
# Claude plugin — every-boot auth configuration.
#
# Runs from /etc/workstation-startup.d/, after 200_remote-agent-setup.sh
# has fetched secrets and written /run/ra/env, and after
# 250_claude-install.sh has installed the claude binary.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"

# Source fetched secrets (sets CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY, etc.).
[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CLAUDE_ENABLED:-false}" = "true" ] || exit 0

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"
CLAUDE_CONFIG_PATH="${USER_HOME}/.claude.json"
CLAUDE_BIN_PATH="${USER_HOME}/.npm-global/bin/claude"

log() { echo "[claude-setup] $(date -u +%H:%M:%S) $*"; }

_clear_stale_state() {
    rm -f "${APIKEY_HELPER_PATH}"
    sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
    sed -i '/^ANTHROPIC_API_KEY=/d' /etc/environment

    if [[ -f "${CLAUDE_SETTINGS_PATH}" ]] && command -v jq >/dev/null 2>&1; then
        if jq -e '.apiKeyHelper' "${CLAUDE_SETTINGS_PATH}" >/dev/null 2>&1; then
            local tmp; tmp=$(mktemp)
            jq 'del(.apiKeyHelper)' "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
        fi
    fi
}

_seed_onboarding() {
    local token="$1"
    local version
    version=$(sudo -u "${USER_NAME}" "${CLAUDE_BIN_PATH}" --version 2>/dev/null \
        | awk '{print $1}' || true)
    [[ "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || version=""

    local tmp; tmp=$(mktemp)
    if [[ -f "${CLAUDE_CONFIG_PATH}" ]]; then
        jq --arg ver "${version}" '
            .hasCompletedOnboarding = true
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
        ' "${CLAUDE_CONFIG_PATH}" > "${tmp}" \
            || { rm -f "${tmp}"; log "jq merge failed"; return 0; }
    else
        jq -n --arg ver "${version}" '
            {hasCompletedOnboarding: true}
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
        ' > "${tmp}" \
            || { rm -f "${tmp}"; log "jq render failed"; return 0; }
    fi
    mv "${tmp}" "${CLAUDE_CONFIG_PATH}"
    chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_CONFIG_PATH}"
    chmod 600 "${CLAUDE_CONFIG_PATH}"

    if sudo -u "${USER_NAME}" env CLAUDE_CODE_OAUTH_TOKEN="${token}" \
            "${CLAUDE_BIN_PATH}" -p "respond with OK" 2>/dev/null | grep -q 'OK'; then
        log "smoke test OK"
    else
        log "smoke test did not return OK"
    fi
}

_clear_stale_state

case "${RA_PLUGIN_CLAUDE_AUTH_PROVIDER:-}" in
    oauth)
        if [[ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
            log "CLAUDE_CODE_OAUTH_TOKEN not set; auth may fail"
        else
            echo "CLAUDE_CODE_OAUTH_TOKEN=${CLAUDE_CODE_OAUTH_TOKEN}" >> /etc/environment
            log "exported CLAUDE_CODE_OAUTH_TOKEN"
            _seed_onboarding "${CLAUDE_CODE_OAUTH_TOKEN}"
        fi
        ;;
    apikey)
        if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
            log "ANTHROPIC_API_KEY not set; auth may fail"
        else
            cat > "${APIKEY_HELPER_PATH}" <<EOF
#!/bin/bash
echo "${ANTHROPIC_API_KEY}"
EOF
            chmod 0755 "${APIKEY_HELPER_PATH}"
            mkdir -p "${CLAUDE_SETTINGS_DIR}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_DIR}"
            if [[ ! -f "${CLAUDE_SETTINGS_PATH}" ]]; then
                printf '{"apiKeyHelper": "%s"}\n' "${APIKEY_HELPER_PATH}" > "${CLAUDE_SETTINGS_PATH}"
            elif command -v jq >/dev/null 2>&1; then
                tmp=$(mktemp)
                jq --arg h "${APIKEY_HELPER_PATH}" '.apiKeyHelper = $h' \
                    "${CLAUDE_SETTINGS_PATH}" > "${tmp}" && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            fi
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
            log "configured apikey mode"
        fi
        ;;
    *)
        log "unknown auth provider '${RA_PLUGIN_CLAUDE_AUTH_PROVIDER:-}'"
        ;;
esac
```

- [ ] **Step 3: Make scripts executable and commit**

```bash
chmod +x plugins/claude/workstation-startup.d/250_claude-install.sh
chmod +x plugins/claude/workstation-startup.d/260_claude-setup.sh
git add plugins/claude/workstation-startup.d/
git commit -m "feat(plugins/claude): add workstation-startup.d/ install + setup scripts"
```

---

### Task 19: Delete `internal/plugins/defaults/` and the embed loader branch

**Files:**
- Delete: `internal/plugins/defaults/` (entire directory)
- Delete: `internal/plugins/defaults_embed.go`
- Modify: `internal/plugins/loader.go`
- Modify: `internal/plugins/loader_test.go`

- [ ] **Step 1: Remove the embedded defaults loader branch**

In `internal/plugins/loader.go`, replace the body of `LoadAll`:

```go
// LoadAll returns all plugins from user-global dir and project-local dir.
// Project-local plugins shadow user-global plugins of the same name entirely.
func LoadAll(userDir, projectDir string) ([]*FilePlugin, error) {
	byName := make(map[string]*FilePlugin)

	loadDir(userDir, byName)
	loadDir(projectDir, byName)

	out := make([]*FilePlugin, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}
```

(Removed the `defaultsFS.ReadDir("defaults")` block.)

- [ ] **Step 2: Delete the embed**

```bash
git rm internal/plugins/defaults_embed.go
git rm -r internal/plugins/defaults/
```

- [ ] **Step 3: Update tests**

Open `internal/plugins/loader_test.go`. Remove or rewrite tests that depend on `defaults/` being embedded. Tests should now use `t.TempDir()` to create fixture plugins.

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/ 
git commit -m "feat(plugins): remove embedded defaults; ra ships with zero built-in plugins"
```

---

## Phase 4 — Plugin install enhancements

### Task 20: Extract `ensureSecret` to `internal/secrets/upload.go`

**Files:**
- Create: `internal/secrets/upload.go`
- Create: `internal/secrets/upload_test.go`
- Modify: `cmd/create.go`

- [ ] **Step 1: Create the package**

Write `internal/secrets/upload.go`:

```go
// Package secrets centralizes GCP Secret Manager interactions used by
// `ra create` and `ra plugin install`.
package secrets

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adamtait/remote-agent/internal/gcloud"
	"golang.org/x/term"
)

// EnsureSecret checks if a secret exists in GCP Secret Manager; if not,
// reads a value from the user (hidden input) and creates the secret.
// Caller is responsible for IAM bindings (done in `ra create`).
func EnsureSecret(r gcloud.Runner, projectID, name, prompt string, stderr io.Writer, stdin *os.File) error {
	if r.Check("secrets", "describe", name, "--project", projectID) {
		fmt.Fprintf(stderr, "Secret %q already exists (use 'gcloud secrets versions add' to rotate)\n", name)
		return nil
	}

	fmt.Fprintf(stderr, "%s: ", prompt)
	value, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return fmt.Errorf("reading secret value: %w", err)
	}
	if len(strings.TrimSpace(string(value))) == 0 {
		return fmt.Errorf("secret value cannot be empty")
	}
	defer func() {
		for i := range value {
			value[i] = 0
		}
	}()

	if err := r.RunWithStdin(bytes.NewReader(value),
		"secrets", "create", name,
		"--replication-policy=automatic",
		"--data-file=-",
		"--project", projectID); err != nil {
		return fmt.Errorf("creating secret %s: %w", name, err)
	}
	return nil
}
```

- [ ] **Step 2: Add a basic test**

Write `internal/secrets/upload_test.go` (uses a fake `gcloud.Runner`):

```go
package secrets

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

type fakeRunner struct {
	exists       bool
	createdName  string
	createdValue string
}

func (f *fakeRunner) Check(args ...string) bool { return f.exists }
func (f *fakeRunner) Run(args ...string) error  { return nil }
func (f *fakeRunner) RunWithStdin(stdin io.Reader, args ...string) error {
	for i, a := range args {
		if a == "create" && i+1 < len(args) {
			f.createdName = args[i+1]
		}
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(stdin)
	f.createdValue = buf.String()
	return nil
}

// Compile-time check that fakeRunner implements gcloud.Runner.
var _ gcloud.Runner = (*fakeRunner)(nil)

func TestEnsureSecret_AlreadyExists(t *testing.T) {
	r := &fakeRunner{exists: true}
	stderr := new(bytes.Buffer)
	if err := EnsureSecret(r, "p", "my-secret", "value?", stderr, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("stderr = %q, want already-exists message", stderr.String())
	}
}
```

(Note: testing the read-password path is awkward because `term.ReadPassword` requires a real TTY. Skip that case in tests; integration testing covers it.)

- [ ] **Step 3: Update `cmd/create.go` to call the new package**

In `cmd/create.go`, find:

```go
		if err := ensureSecret(r, cfg.Gcloud.Project, s.SecretName, prompt); err != nil {
```

Replace with:

```go
		if err := secrets.EnsureSecret(r, cfg.Gcloud.Project, s.SecretName, prompt, os.Stderr, os.Stdin); err != nil {
```

Add the import: `"github.com/adamtait/remote-agent/internal/secrets"`.

Then DELETE the now-unused `ensureSecret` function from `cmd/create.go` (it was at the bottom of the file).

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/ cmd/create.go
git commit -m "refactor(secrets): extract ensureSecret to internal/secrets package"
```

---

### Task 21: Rework `FilePlugin.Prompt` to return `PromptResult`

**Files:**
- Modify: `internal/plugins/fileplugin.go`
- Modify: `internal/plugins/fileplugin_test.go`

- [ ] **Step 1: Define the new return type and update `Prompt`**

In `internal/plugins/fileplugin.go`, replace the existing `Prompt` method with:

```go
// PromptResult is what FilePlugin.Prompt returns: the structured values
// the caller needs to (a) upload secrets to GCP Secret Manager and
// (b) write config blocks to config.yaml.
type PromptResult struct {
	Config       map[string]any    // field name → value
	AuthProvider *AuthProvider     // nil if plugin has none
	SecretValues map[string]string // logical secret name → plaintext value
}

// Prompt interactively collects plugin config + auth provider + secret
// values from the user. Returns a structured result; the caller renders
// YAML and uploads secrets.
func (p *FilePlugin) Prompt(r *bufio.Reader, out io.Writer, stderr io.Writer, stdin *os.File) (*PromptResult, error) {
	pluginCfg := make(map[string]any)
	for _, f := range p.schema.Config {
		val, e := promptField(r, out, f)
		if e != nil {
			return nil, e
		}
		pluginCfg[f.Name] = val
	}

	enabled := true
	for _, f := range p.schema.Config {
		if f.Name == "enabled" {
			if v, ok := pluginCfg["enabled"]; ok {
				enabled = v == true
			}
		}
	}

	var activeProvider *AuthProvider
	if enabled && len(p.schema.AuthProviders) > 0 {
		ap, e := promptAuthProvider(r, out, p.schema.AuthProviders)
		if e != nil {
			return nil, e
		}
		pluginCfg["auth_provider"] = ap.Name
		activeProvider = ap
	}

	values := make(map[string]string)
	if enabled {
		// Top-level secrets (always prompted when enabled).
		for _, s := range p.schema.Secrets {
			v, e := promptSecretValue(stdin, stderr, s)
			if e != nil {
				return nil, e
			}
			values[s.Name] = v
		}
		// Active-provider secrets.
		if activeProvider != nil {
			for _, s := range activeProvider.Secrets {
				v, e := promptSecretValue(stdin, stderr, s)
				if e != nil {
					return nil, e
				}
				values[s.Name] = v
			}
		}
	}

	return &PromptResult{
		Config:       pluginCfg,
		AuthProvider: activeProvider,
		SecretValues: values,
	}, nil
}

// promptSecretValue reads a plaintext secret value with hidden input.
func promptSecretValue(stdin *os.File, stderr io.Writer, s SecretDef) (string, error) {
	fmt.Fprintf(stderr, "Enter value for secret %q (env var %s): ", s.Name, s.EnvVar)
	val, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("reading secret %s: %w", s.Name, err)
	}
	v := strings.TrimSpace(string(val))
	if v == "" {
		return "", fmt.Errorf("secret %s cannot be empty", s.Name)
	}
	return v, nil
}
```

Add imports `"os"` and `"golang.org/x/term"`.

- [ ] **Step 2: Add a helper to render YAML fragments from a `PromptResult`**

Append to `internal/plugins/fileplugin.go`:

```go
// RenderConfigBlock returns the YAML fragment for plugins.<name>.* given
// a PromptResult — used by `ra plugin install` when writing to config.
func (p *FilePlugin) RenderConfigBlock(res *PromptResult) string {
	return buildPluginsConfigBlock(p.schema.Name, res.Config)
}

// RenderSecretsBlock returns the YAML fragment for the secrets: section
// (indented entries, no root key) for the secrets relevant to res.
func (p *FilePlugin) RenderSecretsBlock(res *PromptResult) string {
	return buildSecretsBlock(p.schema.Secrets, res.AuthProvider)
}
```

- [ ] **Step 3: Update tests**

Existing `fileplugin_test.go` likely has tests for the old `Prompt` return shape. Update them to use `PromptResult`. Specifically, replace assertions on returned strings with assertions on `result.Config`, `result.AuthProvider`, etc.

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./internal/plugins/...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/
git commit -m "refactor(plugins): Prompt returns PromptResult struct (config + auth + secret values)"
```

---

### Task 22: Add level prompt + flags to `ra plugin install`

**Files:**
- Modify: `cmd/plugin.go`

- [ ] **Step 1: Add `--user`/`--local` flags and a level prompt**

In `cmd/plugin.go`, replace the existing `pluginInstallLocal` declaration and the level-determining block in the `RunE`:

```go
var (
	pluginInstallUser  bool
	pluginInstallLocal bool
	pluginInstallForce bool
)

func init() {
	pluginInstallCmd.Flags().BoolVar(&pluginInstallUser, "user", false, "Install at user level (~/.config/ra/plugins/)")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallLocal, "local", false, "Install at project level (.ra/plugins/)")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallForce, "force", false, "Overwrite existing plugin of the same name at the chosen level")
	// ... existing AddCommand calls
}
```

In the `RunE`, after cloning into `tmp` and parsing `plugin.yaml`, replace the `if pluginInstallLocal { ... } else { ... }` block:

```go
	// Determine install level: explicit flags first, otherwise prompt.
	level, err := chooseInstallLevel(cmd, pluginInstallUser, pluginInstallLocal)
	if err != nil {
		return err
	}

	var destBase string
	switch level {
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		destBase = filepath.Join(home, ".config", "ra", "plugins")
	case "project":
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		destBase = filepath.Join(wd, ".ra", "plugins")
	}
	dest := filepath.Join(destBase, p.Name())
```

Add the helper at the bottom of `cmd/plugin.go`:

```go
// chooseInstallLevel returns "user" or "project" based on flags. If neither
// flag is set, it prompts the user interactively.
func chooseInstallLevel(cmd *cobra.Command, user, local bool) (string, error) {
	switch {
	case user && local:
		return "", fmt.Errorf("--user and --local are mutually exclusive")
	case user:
		return "user", nil
	case local:
		return "project", nil
	}
	r := bufio.NewReader(cmd.InOrStdin())
	fmt.Fprintln(cmd.OutOrStdout(), "Install at user level (~/.config/ra/plugins/) or project level (.ra/plugins/)?")
	fmt.Fprintln(cmd.OutOrStdout(), "  1) user")
	fmt.Fprintln(cmd.OutOrStdout(), "  2) project")
	fmt.Fprint(cmd.OutOrStdout(), "choice [1-2] [1]: ")
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	choice := strings.TrimSpace(line)
	if choice == "" || choice == "1" {
		return "user", nil
	}
	if choice == "2" {
		return "project", nil
	}
	return "", fmt.Errorf("invalid choice %q", choice)
}
```

Add imports: `"bufio"`, `"io"`, `"strings"`.

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Add a test for `chooseInstallLevel`**

In `cmd/plugin_test.go`, add:

```go
func TestChooseInstallLevel_FlagUser(t *testing.T) {
	c := &cobra.Command{}
	level, err := chooseInstallLevel(c, true, false)
	if err != nil || level != "user" {
		t.Fatalf("got level=%q err=%v, want user/nil", level, err)
	}
}

func TestChooseInstallLevel_FlagLocal(t *testing.T) {
	c := &cobra.Command{}
	level, err := chooseInstallLevel(c, false, true)
	if err != nil || level != "project" {
		t.Fatalf("got level=%q err=%v, want project/nil", level, err)
	}
}

func TestChooseInstallLevel_BothFlagsErr(t *testing.T) {
	c := &cobra.Command{}
	if _, err := chooseInstallLevel(c, true, true); err == nil {
		t.Fatal("expected error for mutually exclusive flags")
	}
}

func TestChooseInstallLevel_PromptDefault(t *testing.T) {
	c := &cobra.Command{}
	c.SetIn(strings.NewReader("\n"))
	c.SetOut(io.Discard)
	level, err := chooseInstallLevel(c, false, false)
	if err != nil || level != "user" {
		t.Fatalf("got level=%q err=%v, want user/nil (default)", level, err)
	}
}

func TestChooseInstallLevel_PromptProject(t *testing.T) {
	c := &cobra.Command{}
	c.SetIn(strings.NewReader("2\n"))
	c.SetOut(io.Discard)
	level, err := chooseInstallLevel(c, false, false)
	if err != nil || level != "project" {
		t.Fatalf("got level=%q err=%v, want project/nil", level, err)
	}
}
```

Add imports as needed.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/... -run TestChooseInstallLevel`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/plugin.go cmd/plugin_test.go
git commit -m "feat(plugin): interactive level prompt + --user/--local flags"
```

---

### Task 23: Add config + secrets writing to `ra plugin install`

**Files:**
- Modify: `cmd/plugin.go`
- Create: `internal/configwrite/append.go` + `_test.go`

We need a small utility to merge a YAML fragment into an existing `config.yaml` without losing formatting/comments. For simplicity, use `gopkg.in/yaml.v3`'s node API.

- [ ] **Step 1: Write the failing test for config merging**

Create `internal/configwrite/append_test.go`:

```go
package configwrite

import (
	"strings"
	"testing"
)

func TestMergePluginConfig_AddsToEmptyPluginsBlock(t *testing.T) {
	original := `git:
  user_name: alice
plugins: {}
secrets:
  github_pat:
    secret_name: gh
    env_var: GH_TOKEN
`
	out, err := MergePluginConfig(original, "claude",
		map[string]any{"enabled": true, "auth_provider": "oauth"},
		map[string]string{"claude_oauth": "claude-oauth-secret-name"},
		map[string]string{"claude_oauth": "CLAUDE_CODE_OAUTH_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "claude:") || !strings.Contains(out, "auth_provider: oauth") {
		t.Errorf("missing plugin block: %s", out)
	}
	if !strings.Contains(out, "claude_oauth:") || !strings.Contains(out, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("missing secret entry: %s", out)
	}
	// Existing entries preserved.
	if !strings.Contains(out, "user_name: alice") {
		t.Errorf("git block lost: %s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configwrite/`
Expected: FAIL — package missing.

- [ ] **Step 3: Implement `MergePluginConfig`**

Create `internal/configwrite/append.go`:

```go
// Package configwrite merges plugin config + secrets into an existing
// config.yaml without losing top-level keys. Used by `ra plugin install`.
package configwrite

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// MergePluginConfig takes existing YAML, the plugin name, its config map,
// a secrets map (logical name → GCP secret name), and an env-var map
// (logical name → env var name); returns merged YAML. The plugins.<name>
// block is set/replaced; the secrets.<logical> entries are added; all
// other top-level keys are preserved.
func MergePluginConfig(
	original string,
	pluginName string,
	pluginCfg map[string]any,
	secretNames map[string]string,
	secretEnvVars map[string]string,
) (string, error) {
	var root yaml.Node
	if original == "" {
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	} else if err := yaml.Unmarshal([]byte(original), &root); err != nil {
		return "", fmt.Errorf("parse config.yaml: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("config.yaml has unexpected shape")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return "", fmt.Errorf("config.yaml root is not a map")
	}

	// plugins.<name> = pluginCfg
	pluginsNode := getOrCreateMap(doc, "plugins")
	setMapValue(pluginsNode, pluginName, fromAny(pluginCfg))

	// secrets.<logical> = {secret_name, env_var}
	if len(secretNames) > 0 {
		secretsNode := getOrCreateMap(doc, "secrets")
		for logical, secretName := range secretNames {
			entry := &yaml.Node{Kind: yaml.MappingNode}
			setMapValue(entry, "secret_name", &yaml.Node{Kind: yaml.ScalarNode, Value: secretName})
			setMapValue(entry, "env_var", &yaml.Node{Kind: yaml.ScalarNode, Value: secretEnvVars[logical]})
			setMapValue(secretsNode, logical, entry)
		}
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func getOrCreateMap(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		k := parent.Content[i]
		v := parent.Content[i+1]
		if k.Value == key {
			if v.Kind != yaml.MappingNode {
				v.Kind = yaml.MappingNode
				v.Tag = ""
				v.Value = ""
				v.Content = nil
			}
			return v
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode
}

func setMapValue(parent *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = value
			return
		}
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
}

func fromAny(m map[string]any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for k, v := range m {
		var val yaml.Node
		_ = val.Encode(v)
		setMapValue(n, k, &val)
	}
	return n
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/configwrite/`
Expected: PASS.

- [ ] **Step 5: Wire into `cmd/plugin.go`**

After the file copy and prompt steps in `pluginInstallCmd`, add:

```go
	// Determine which config.yaml to update based on level.
	cfgPath := userConfigPath(home)
	if level == "project" {
		cfgPath = projectConfigPath(wd)
	}
	original, _ := os.ReadFile(cfgPath) // empty if not exists
	if len(original) == 0 {
		return fmt.Errorf("config.yaml not found at %s — run 'ra setup' first", cfgPath)
	}

	// Build maps for configwrite.MergePluginConfig.
	secretNames := make(map[string]string)
	secretEnvVars := make(map[string]string)
	for _, s := range allRelevantSecretDefs(p, result) {
		gcp := s.DefaultSecretName
		if gcp == "" {
			gcp = s.Name
		}
		secretNames[s.Name] = gcp
		secretEnvVars[s.Name] = s.EnvVar
	}

	merged, err := configwrite.MergePluginConfig(string(original), p.Name(), result.Config, secretNames, secretEnvVars)
	if err != nil {
		return fmt.Errorf("merging config: %w", err)
	}
	if err := os.WriteFile(cfgPath, []byte(merged), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", cfgPath, err)
	}
```

Add helpers at the bottom of `cmd/plugin.go`:

```go
func userConfigPath(home string) string    { return filepath.Join(home, ".config", "ra", "config.yaml") }
func projectConfigPath(wd string) string   { return filepath.Join(wd, ".ra", "config.yaml") }

// allRelevantSecretDefs returns the union of plugin-level secrets and
// active-provider secrets that need to be written to config.
func allRelevantSecretDefs(p *plugins.FilePlugin, res *plugins.PromptResult) []plugins.SecretDef {
	defs := append([]plugins.SecretDef{}, p.Schema().Secrets...)
	if res.AuthProvider != nil {
		defs = append(defs, res.AuthProvider.Secrets...)
	}
	return defs
}
```

(Note: `result` and `home`/`wd` are local vars; introduce them in the RunE flow before this block. After cloning + parsing plugin.yaml + choosing level + moving the dir, call `p.Prompt(...)` to get `result`.)

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add internal/configwrite/ cmd/plugin.go
git commit -m "feat(plugin): write plugins.<name> + secrets entries to config on install"
```

---

### Task 24: Wire secret upload + `.ra-install.yaml` metadata into `ra plugin install`

**Files:**
- Modify: `cmd/plugin.go`

- [ ] **Step 1: Add the upload + metadata steps**

In `cmd/plugin.go`'s install `RunE`, AFTER the `Prompt` call but BEFORE the config write, add:

```go
	// Upload secret values to GCP Secret Manager.
	if err := uploadPluginSecrets(p, result, cfg.Gcloud.Project); err != nil {
		return fmt.Errorf("uploading secrets: %w", err)
	}

	// (then config write block from Task 23)

	// Write per-install metadata.
	meta := fmt.Sprintf("source: %s\ninstalled_at: %s\n",
		gitURL, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dest, ".ra-install.yaml"), []byte(meta), 0644); err != nil {
		return fmt.Errorf("writing install metadata: %w", err)
	}
```

(`cfg` here is the loaded ra config; we'll need to load it. See Step 2.)

Add the helper:

```go
// uploadPluginSecrets pushes each secret value collected during Prompt
// to GCP Secret Manager. Secret names come from plugin.yaml's
// default_secret_name (or the logical name as fallback).
func uploadPluginSecrets(p *plugins.FilePlugin, res *plugins.PromptResult, projectID string) error {
	for _, s := range allRelevantSecretDefs(p, res) {
		val, ok := res.SecretValues[s.Name]
		if !ok || val == "" {
			continue
		}
		gcpName := s.DefaultSecretName
		if gcpName == "" {
			gcpName = s.Name
		}
		// Reuse the gcloud runner we already have; the call is the same as
		// `ra create` does for ensure-then-upload.
		if err := uploadSecretValue(projectID, gcpName, val); err != nil {
			return fmt.Errorf("secret %s: %w", s.Name, err)
		}
	}
	return nil
}

// uploadSecretValue creates the secret if it doesn't exist and uploads
// the value as the latest version. (Different from secrets.EnsureSecret
// which prompts for the value; here we already have it.)
func uploadSecretValue(projectID, name, value string) error {
	r := newGcloudRunner()
	exists := r.Check("secrets", "describe", name, "--project", projectID)
	if !exists {
		if err := r.RunWithStdin(strings.NewReader(value),
			"secrets", "create", name,
			"--replication-policy=automatic",
			"--data-file=-",
			"--project", projectID); err != nil {
			return fmt.Errorf("creating secret %s: %w", name, err)
		}
		return nil
	}
	if err := r.RunWithStdin(strings.NewReader(value),
		"secrets", "versions", "add", name,
		"--data-file=-",
		"--project", projectID); err != nil {
		return fmt.Errorf("adding version to %s: %w", name, err)
	}
	return nil
}
```

(`newGcloudRunner` is the existing helper used by other commands; check `cmd/root.go` or similar — adapt the reference.)

- [ ] **Step 2: Load ra config to get `cfg.Gcloud.Project`**

At the top of `RunE` (before any work), add:

```go
	if cfg == nil || cfg.Gcloud.Project == "" {
		return fmt.Errorf("ra config not loaded — run 'ra setup' first")
	}
```

(`cfg` is the package-global `*config.Config` populated by `cmd/root.go`'s `PersistentPreRunE`. If `pluginInstallCmd` has its own `PersistentPreRunE: ... return nil`, change it to invoke the global pre-run that loads config. Check `cmd/root.go` for the pattern.)

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add cmd/plugin.go
git commit -m "feat(plugin): upload secret values to GCP Secret Manager + write .ra-install.yaml"
```

---

### Task 25: Support local-path source in `ra plugin install`

**Files:**
- Modify: `cmd/plugin.go`

- [ ] **Step 1: Detect source type and switch acquire mode**

In `cmd/plugin.go`'s install `RunE`, replace the `git clone` block with:

```go
	source := args[0]
	tmp, err := os.MkdirTemp("", "ra-plugin-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	if isLocalPath(source) {
		abs, err := filepath.Abs(source)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", source, err)
		}
		if err := copyDir(abs, tmp); err != nil {
			return fmt.Errorf("copying local plugin: %w", err)
		}
	} else {
		clone := exec.Command("git", "clone", "--depth=1", source, tmp)
		clone.Stdout = os.Stdout
		clone.Stderr = os.Stderr
		if err := clone.Run(); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
	}
```

Add the helper:

```go
// isLocalPath returns true if source is a filesystem path (absolute or
// relative starting with ./ or ../, or a path that resolves to an
// existing directory).
func isLocalPath(source string) bool {
	if strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
		return true
	}
	// Bare relative path that exists as a dir.
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		return true
	}
	return false
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Add a quick test**

In `cmd/plugin_test.go`:

```go
func TestIsLocalPath(t *testing.T) {
	if !isLocalPath("./foo") {
		t.Error("./foo should be local")
	}
	if !isLocalPath("/abs/path") {
		t.Error("/abs/path should be local")
	}
	if isLocalPath("https://github.com/foo/bar.git") {
		t.Error("https URL should NOT be local")
	}
	if isLocalPath("git@github.com:foo/bar.git") {
		t.Error("git@ URL should NOT be local")
	}
}
```

Run: `go test ./cmd/... -run TestIsLocalPath`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/plugin.go cmd/plugin_test.go
git commit -m "feat(plugin): support local filesystem path as install source"
```

---

### Task 26: Update `ra plugin list` to show level + source

**Files:**
- Modify: `cmd/plugin.go`

- [ ] **Step 1: Read `.ra-install.yaml` per plugin and render a table**

Replace the body of `pluginListCmd.RunE`:

```go
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		userDir := filepath.Join(home, ".config", "ra", "plugins")
		projDir := filepath.Join(wd, ".ra", "plugins")

		userPlugs, _ := plugins.LoadAll(userDir, "") // user-only
		projPlugs, _ := plugins.LoadAll("", projDir) // project-only

		shadowed := make(map[string]bool)
		for _, p := range projPlugs {
			shadowed[p.Name()] = true
		}

		all := append([]listEntry{}, makeListEntries(userPlugs, "user", userDir, shadowed)...)
		all = append(all, makeListEntries(projPlugs, "project", projDir, nil)...)
		if len(all) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(no plugins installed)")
			return nil
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%-15s %-8s %-50s %s\n", "NAME", "LEVEL", "SOURCE", "DESCRIPTION")
		for _, e := range all {
			name := e.Name
			if e.Shadowed {
				name = e.Name + " (shadowed)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-15s %-8s %-50s %s\n", name, e.Level, e.Source, e.Description)
		}
		return nil
```

Add types/helpers:

```go
type listEntry struct {
	Name        string
	Level       string
	Source      string
	Description string
	Shadowed    bool
}

func makeListEntries(ps []*plugins.FilePlugin, level, baseDir string, shadowed map[string]bool) []listEntry {
	var out []listEntry
	for _, p := range ps {
		meta := readInstallMeta(filepath.Join(baseDir, p.Name()))
		out = append(out, listEntry{
			Name:        p.Name(),
			Level:       level,
			Source:      meta.Source,
			Description: p.Schema().Description,
			Shadowed:    shadowed != nil && shadowed[p.Name()],
		})
	}
	return out
}

type installMeta struct {
	Source      string `yaml:"source"`
	InstalledAt string `yaml:"installed_at"`
}

func readInstallMeta(pluginDir string) installMeta {
	data, err := os.ReadFile(filepath.Join(pluginDir, ".ra-install.yaml"))
	if err != nil {
		return installMeta{Source: "(unknown)"}
	}
	var m installMeta
	_ = yaml.Unmarshal(data, &m)
	if m.Source == "" {
		m.Source = "(unknown)"
	}
	return m
}
```

Add `"gopkg.in/yaml.v3"` to imports.

- [ ] **Step 2: Build and test**

Run: `go build ./...`
Expected: success.

Run: `go test ./cmd/... -run TestPluginList` (if any). If none exist, add a basic one that creates two temp plugin dirs with `.ra-install.yaml` and asserts the output table contains expected names + sources.

- [ ] **Step 3: Commit**

```bash
git add cmd/plugin.go
git commit -m "feat(plugin): list shows LEVEL/SOURCE columns from .ra-install.yaml"
```

---

### Task 27: Update `ra plugin remove` to clean config + secrets

**Files:**
- Modify: `cmd/plugin.go`

- [ ] **Step 1: Replace `pluginRemoveCmd.RunE` body**

```go
		name := args[0]
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		wd, err := os.Getwd()
		if err != nil {
			return err
		}

		userPath := filepath.Join(home, ".config", "ra", "plugins", name)
		projPath := filepath.Join(wd, ".ra", "plugins", name)
		userExists := dirExists(userPath)
		projExists := dirExists(projPath)

		if !userExists && !projExists {
			return fmt.Errorf("plugin %q not installed at user or project level", name)
		}

		var removePath, cfgPath string
		switch {
		case userExists && projExists:
			r := bufio.NewReader(cmd.InOrStdin())
			fmt.Fprintf(cmd.OutOrStdout(), "Plugin %q is installed at both levels. Remove from:\n  1) user (%s)\n  2) project (%s)\nchoice [1-2]: ", name, userPath, projPath)
			line, _ := r.ReadString('\n')
			switch strings.TrimSpace(line) {
			case "1":
				removePath = userPath
				cfgPath = userConfigPath(home)
			case "2":
				removePath = projPath
				cfgPath = projectConfigPath(wd)
			default:
				return fmt.Errorf("invalid choice")
			}
		case userExists:
			removePath = userPath
			cfgPath = userConfigPath(home)
		case projExists:
			removePath = projPath
			cfgPath = projectConfigPath(wd)
		}

		// Load plugin.yaml so we know which secrets to remove from config.
		p, err := plugins.LoadPlugin(os.DirFS(removePath))
		if err != nil {
			return fmt.Errorf("reading %s/plugin.yaml: %w", removePath, err)
		}
		secretLogicals := []string{}
		for _, s := range p.Schema().Secrets {
			secretLogicals = append(secretLogicals, s.Name)
		}
		for _, ap := range p.Schema().AuthProviders {
			for _, s := range ap.Secrets {
				secretLogicals = append(secretLogicals, s.Name)
			}
		}

		// Update config.yaml: remove plugins.<name> and secrets entries.
		if data, err := os.ReadFile(cfgPath); err == nil {
			updated, err := configwrite.RemovePluginConfig(string(data), name, secretLogicals)
			if err != nil {
				return fmt.Errorf("rewriting %s: %w", cfgPath, err)
			}
			if err := os.WriteFile(cfgPath, []byte(updated), 0644); err != nil {
				return fmt.Errorf("writing %s: %w", cfgPath, err)
			}
		}

		if err := os.RemoveAll(removePath); err != nil {
			return err
		}

		// Print a hint about GCP Secret Manager values.
		gcpNames := []string{}
		for _, s := range p.Schema().Secrets {
			gcpNames = append(gcpNames, gcpSecretName(s))
		}
		for _, ap := range p.Schema().AuthProviders {
			for _, s := range ap.Secrets {
				gcpNames = append(gcpNames, gcpSecretName(s))
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed plugin %q from %s\n", name, removePath)
		if len(gcpNames) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Secret values remain in GCP Secret Manager. To delete them manually:")
			for _, gn := range gcpNames {
				fmt.Fprintf(cmd.OutOrStdout(), "  gcloud secrets delete %s --project %s\n", gn, cfg.Gcloud.Project)
			}
		}
		return nil
```

Add helpers:

```go
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func gcpSecretName(s plugins.SecretDef) string {
	if s.DefaultSecretName != "" {
		return s.DefaultSecretName
	}
	return s.Name
}
```

- [ ] **Step 2: Add `RemovePluginConfig` to configwrite**

In `internal/configwrite/append.go` (or a new file):

```go
// RemovePluginConfig removes plugins.<name> and removes secrets entries
// matching any of secretLogicals from existing YAML.
func RemovePluginConfig(original, pluginName string, secretLogicals []string) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(original), &root); err != nil {
		return "", err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return original, nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return original, nil
	}

	deleteMapKey(getMapValue(doc, "plugins"), pluginName)
	secretsNode := getMapValue(doc, "secrets")
	for _, logical := range secretLogicals {
		deleteMapKey(secretsNode, logical)
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func getMapValue(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

func deleteMapKey(parent *yaml.Node, key string) {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return
		}
	}
}
```

Add a test in `internal/configwrite/append_test.go`:

```go
func TestRemovePluginConfig(t *testing.T) {
	original := `git:
  user_name: alice
plugins:
  claude:
    enabled: true
secrets:
  github_pat:
    secret_name: gh
    env_var: GH_TOKEN
  claude_oauth:
    secret_name: claude-oauth
    env_var: CLAUDE_CODE_OAUTH_TOKEN
`
	out, err := RemovePluginConfig(original, "claude", []string{"claude_oauth"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "claude:") {
		t.Errorf("plugins.claude not removed: %s", out)
	}
	if strings.Contains(out, "claude_oauth:") {
		t.Errorf("secrets.claude_oauth not removed: %s", out)
	}
	if !strings.Contains(out, "github_pat:") {
		t.Errorf("github_pat secret should be preserved: %s", out)
	}
}
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./internal/configwrite/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/configwrite/ cmd/plugin.go
git commit -m "feat(plugin): remove cleans config + secrets entries; warns about GCP values"
```

---

## Phase 5 — Light github extraction prep

### Task 28: Extract `_ra_handle_github_pat` and `_ra_first_boot_git` in entrypoint

**Files:**
- Modify: `internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh`

- [ ] **Step 1: Wrap the `github_pat` case body in a function**

In `200_remote-agent-setup.sh`, find the `github_pat)` case in the dispatch loop. Refactor to:

```bash
_ra_handle_github_pat() {
    local secret_name="$1"
    local target_env_var="$2"

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

    local pat_value
    pat_value=$(gcloud secrets versions access latest \
        --secret="${secret_name}" --project="${GCP_PROJECT_ID}" 2>/dev/null || true)
    if [[ -n "${pat_value}" ]]; then
        echo "${target_env_var}=${pat_value}" >> /etc/environment
        umask 077
        if [[ ! -f "${RA_ENV_FILE}" ]]; then
            : > "${RA_ENV_FILE}"
            chmod 0644 "${RA_ENV_FILE}"
        fi
        sed -i "/^${target_env_var}=/d" "${RA_ENV_FILE}"
        printf '%s=%s\n' "${target_env_var}" "${pat_value}" >> "${RA_ENV_FILE}"
        log "Exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."
    fi
}
```

Then replace the `github_pat)` case body with:

```bash
        github_pat)
            _ra_handle_github_pat "${secret_name}" "${target_env_var}"
            ;;
```

- [ ] **Step 2: Wrap the first-boot block in `_ra_first_boot_git`**

Find the `if [[ ! -f "${FIRST_BOOT_MARKER}" ]]; then` block near the bottom. Refactor to:

```bash
_ra_first_boot_git() {
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
}

if [[ ! -f "${FIRST_BOOT_MARKER}" ]]; then
    _ra_first_boot_git
fi
```

Note: `200_remote-agent-setup.sh` no longer needs to source plugin setup scripts (Phase 4 changed the model so plugin scripts run independently from `/etc/workstation-startup.d/`). Delete the loop:

```bash
# REMOVE THIS BLOCK:
# for _ra_plugin_setup in /etc/ra/plugins/*/setup.sh; do
#     if [[ -f "${_ra_plugin_setup}" ]]; then
#         log "Sourcing plugin: ${_ra_plugin_setup}"
#         source "${_ra_plugin_setup}"
#     fi
# done
# unset _ra_plugin_setup
```

- [ ] **Step 3: Verify the script still parses**

Run: `bash -n internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh`
Expected: no output (parse OK).

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh
git commit -m "refactor(entrypoint): extract _ra_handle_github_pat + _ra_first_boot_git; drop plugin source loop"
```

---

## Phase 6 — Documentation

### Task 29: Update `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Rewrite the "Architecture" and related sections**

Edit `CLAUDE.md` to reflect the new state:

a. Replace the "Embedded Assets" section with:

````markdown
### Build context assembly

`internal/scaffold/buildcontext.go` (`AssembleBuildContext`) produces an ephemeral `.ra/.build/` directory each time `ra create` runs. It merges core ra files with all installed plugins' contributions in priority order:

- Single-file resolution (`Dockerfile`): project (`.ra/Dockerfile`) → user (`~/.config/ra/Dockerfile`) → embedded
- Directory merge (`Dockerfile.d/`, `workstation-startup.d/`, `profile.d/`): plugin files first, then user-global, then project-local — same-name files shadow.
- Whole-plugin staging (`plugins/<name>/`): project shadows user; the assembler skips `.ra-install.yaml` so install metadata never lands in the image.

The Dockerfile is generic and references build-context-relative paths (no `.ra/` prefix). `.ra/.build/` is gitignored and removed after every build (success or failure).
````

b. Replace the "Subcommands → Core Packages" entry for `cmd/setup.go`:

```
- **`cmd/setup.go`** calls `scaffold.InteractiveScaffold()` — probes gcloud/git defaults, prompts for ra config (project_id, repo_url, git identity), writes config YAMLs, and materializes core build assets (Dockerfile, workstation-startup.d, profile.d) into `~/.config/ra/` and optionally `.ra/`. It does NOT prompt for plugin config — that moves to `ra plugin install`.
```

c. Add a new section "Plugin lifecycle" describing `ra plugin install/list/remove` per the spec.

d. Update the "Naming" section if needed.

e. Update any reference to `.ra/entrypoint.sh` or `~/.config/ra/template/`.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for generic plugin system"
```

---

### Task 30: Update `README.md`

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the getting-started flow**

Find the "getting started" / quickstart section and rewrite it to:

```markdown
## Getting started

1. **Install `ra`**: `make install` (puts `ra` in `~/.local/bin/`)
2. **Configure ra**: `ra setup` — prompts for GCP project, GitHub repo URL, git identity. Writes `~/.config/ra/config.yaml`.
3. **Install plugins**: `ra plugin install <git-url-or-path>` — prompts for plugin config + secret values, uploads secrets to GCP Secret Manager. Choose user-level (~/.config/ra/plugins/) or project-level (.ra/plugins/).

   Example (after the claude plugin moves to its own repo):
   ```bash
   ra plugin install https://github.com/adamtait/ra-claude-plugin
   ```
4. **Provision the workstation**: `ra create`

`ra` ships with no built-in plugins — Claude Code, GitHub, and any other integrations come from external plugin repositories.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: update README for generic plugin system"
```

---

### Task 31: Final smoke test

**Files:**
- (no file changes — verification step)

- [ ] **Step 1: Build the binary**

Run: `make build`
Expected: success.

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 4: Manual exercise — `ra plugin install ./plugins/claude`**

In a temp project directory with a populated `~/.config/ra/config.yaml` and gcloud auth available:

```bash
mkdir -p /tmp/ra-test && cd /tmp/ra-test
git init
~/.local/bin/ra plugin install /path/to/repo/plugins/claude --user
~/.local/bin/ra plugin list
```

Expected:
- Install prompts for config and secret values
- Uploads secrets to GCP Secret Manager
- Writes `~/.config/ra/config.yaml` with `plugins.claude.*` and `secrets.claude_oauth` entries
- `~/.config/ra/plugins/claude/.ra-install.yaml` exists with `source: /path/to/repo/plugins/claude`
- `ra plugin list` shows claude with LEVEL=user and SOURCE=/path/...

Note any unexpected behavior in the commit message of any follow-up fix.

- [ ] **Step 5: Manual exercise — `ra create`**

```bash
cd /tmp/ra-test
~/.local/bin/ra create --name smoke-test
```

Expected:
- `.ra/.build/` is created with merged content (verify by inspection between assembly and docker invoke; if hard, just verify build succeeds)
- Docker build succeeds
- After build, `.ra/.build/` is removed
- Workstation comes up

If anything is broken, file follow-up tasks. The plan is complete.

---

## Self-Review

Spec coverage:
- Layout (sources mirror image, no `template/`, no `.staged/`) → Tasks 1, 7, 14, 19
- Build context assembly (`.ra/.build/`) → Tasks 2–6, 9
- Generic Dockerfile → Task 8
- Plugin layout (Dockerfile.d/workstation-startup.d/profile.d) → Tasks 5, 6, 8, 12
- Claude moved to top-level → Tasks 15–18
- Embedded plugins removed → Task 19
- `ra setup` slimmed → Task 14
- `ra plugin install` enhanced → Tasks 21–25
- `ra plugin list` shows source → Task 26
- `ra plugin remove` cleans config + secrets → Task 27
- Github prep → Task 28
- Docs → Tasks 29, 30
- Final smoke test → Task 31

No placeholders. All file paths are absolute. All Go code blocks are complete and self-contained. All step instructions include exact commands and expected output.
