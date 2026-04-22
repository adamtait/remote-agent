# ws Project Scaffolding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ws` work in any git root on a machine where it's installed, and let users (humans and AI) fully customize the Dockerfile and entrypoint per project via a scaffolded `.ws/` directory.

**Architecture:** Embed the canonical Dockerfile and entrypoint.sh in the `ws` binary via `go:embed`. A new `ws setup scaffold` command materializes them into `./.ws/`. The per-project config moves from `.ws.yaml` at the project root to `.ws/config.yaml`, with auto-migration of legacy files. `ws create` gets a preflight check and builds with `-f .ws/Dockerfile` using the project root as docker context.

**Tech Stack:** Go 1.22+, cobra, `go:embed`, gopkg.in/yaml.v3, docker buildx, gcloud CLI.

---

## File Structure

### New files

| File | Responsibility |
|---|---|
| `internal/scaffold/assets/Dockerfile` | Canonical default Dockerfile, adjusted so `COPY .ws/entrypoint.sh ...` works with project-root build context. Single source of truth for the embedded default. |
| `internal/scaffold/assets/entrypoint.sh` | Canonical default entrypoint.sh. Identical to the current top-level `entrypoint.sh`. |
| `internal/scaffold/embedded.go` | `go:embed` directives exposing `embeddedDockerfile` and `embeddedEntrypoint` as package-level `[]byte`. |
| `internal/scaffold/embedded_test.go` | Sanity: both blobs non-empty, Dockerfile contains `COPY .ws/entrypoint.sh`, entrypoint starts with `#!/bin/bash` and contains `set -euo pipefail`. |
| `internal/scaffold/source.go` | `ResolveSource(home, name)` → `(bytes, sourceLabel, error)`. Checks `~/.config/ws/template/<name>` first; falls back to embedded. |
| `internal/scaffold/source_test.go` | Table-driven tests for user-template present / absent / partial, and unreadable-file error propagation. |
| `internal/scaffold/scaffold_assets.go` | `ScaffoldAssets(opts)` writes `.ws/Dockerfile` and `.ws/entrypoint.sh` atomically. Honors `--force`, `--dry-run`. Creates `.ws/` at 0755. Uses mode 0644 for Dockerfile, 0755 for entrypoint.sh. |
| `internal/scaffold/scaffold_assets_test.go` | Unit tests for the above — absent target, existing without force, existing with force, dry-run, correct modes, user-template precedence, partial scaffold when one file already exists. |
| `internal/config/migrate.go` | `MigrateLegacyProjectConfig(dir)` — if `dir/.ws.yaml` exists and `dir/.ws/config.yaml` does not, moves it atomically. Errors if both exist. |
| `internal/config/migrate_test.go` | Tests for success, conflict (both exist), source-missing (no-op), and target parent dir auto-created. |
| `cmd/scaffold.go` | Cobra wiring for `ws setup scaffold`. Flags: `--force`, `--dry-run`. Skips root's `PersistentPreRunE` (setup runs in fresh repos). |
| `cmd/scaffold_test.go` | End-to-end tests for `ws setup scaffold`. |

### Modified files

| File | What changes |
|---|---|
| `internal/scaffold/scaffold.go` | `InteractiveScaffold` writes `.ws/config.yaml` instead of `.ws.yaml` and prompts `Scaffold customizable .ws/Dockerfile and .ws/entrypoint.sh? [Y/n]` before exiting. `TemplateScaffold` writes `.ws/config.yaml` and unconditionally scaffolds `.ws/Dockerfile`/`.ws/entrypoint.sh` (unless `--global-only`). |
| `internal/scaffold/scaffold_test.go` | Assertions updated to check `.ws/config.yaml` (not `.ws.yaml`). |
| `internal/scaffold/templates.go` | Commented YAML template header mentions `.ws/config.yaml`. |
| `internal/scaffold/templates_test.go` | Updated to reflect new comment text. |
| `internal/config/discover.go` | `FindProjectConfig` walks up looking for `.ws/config.yaml`. At each level, if `.ws.yaml` is present without `.ws/config.yaml`, invoke `MigrateLegacyProjectConfig`. Error when both exist at the same level. |
| `internal/config/discover_test.go` | Tests updated to the new primary path; new tests for migration, conflict, and both-present error. |
| `cmd/setup.go` | Flag descriptions mention `.ws/config.yaml` instead of `.ws.yaml`. `setupCmd` run ends with a scaffold prompt (delegated to `scaffold.InteractiveScaffold` which already returns cleanly). |
| `cmd/setup_test.go` | Assertions updated to new paths; new tests for scaffold prompt (y/n) and `ws setup template` writing `.ws/` assets. |
| `cmd/create.go` | Preflight block before `docker buildx build`. Build args become `-f .ws/Dockerfile` with `.` as context. |
| `cmd/create_test.go` | **New file** (none exists today) — preflight tests using fake docker runner. |
| `Dockerfile` | **Deleted** in the dogfood task. |
| `entrypoint.sh` | **Deleted** in the dogfood task. |
| `.ws/config.yaml`, `.ws/Dockerfile`, `.ws/entrypoint.sh` | **Created** in the dogfood task by running the new `ws` binary against this repo. |

---

## Task 1: Embed default build assets

**Files:**
- Create: `internal/scaffold/assets/Dockerfile`
- Create: `internal/scaffold/assets/entrypoint.sh`
- Create: `internal/scaffold/embedded.go`
- Test: `internal/scaffold/embedded_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/scaffold/embedded_test.go`:

```go
package scaffold

import (
	"strings"
	"testing"
)

func TestEmbeddedDockerfile_NonEmpty(t *testing.T) {
	if len(embeddedDockerfile) == 0 {
		t.Fatal("embeddedDockerfile is empty")
	}
}

func TestEmbeddedDockerfile_HasEntrypointCopyPath(t *testing.T) {
	s := string(embeddedDockerfile)
	want := "COPY .ws/entrypoint.sh"
	if !strings.Contains(s, want) {
		t.Errorf("embedded Dockerfile missing %q; got:\n%s", want, s)
	}
}

func TestEmbeddedEntrypoint_NonEmpty(t *testing.T) {
	if len(embeddedEntrypoint) == 0 {
		t.Fatal("embeddedEntrypoint is empty")
	}
}

func TestEmbeddedEntrypoint_HasShebangAndStrictMode(t *testing.T) {
	s := string(embeddedEntrypoint)
	if !strings.HasPrefix(s, "#!/bin/bash") {
		t.Errorf("embedded entrypoint.sh should start with '#!/bin/bash', got: %s", strings.SplitN(s, "\n", 2)[0])
	}
	if !strings.Contains(s, "set -euo pipefail") {
		t.Error("embedded entrypoint.sh should contain 'set -euo pipefail'")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scaffold/ -run TestEmbedded -v`
Expected: FAIL with `undefined: embeddedDockerfile` / `undefined: embeddedEntrypoint` (package does not compile).

- [ ] **Step 3: Copy entrypoint.sh unchanged into assets**

```bash
mkdir -p internal/scaffold/assets
cp entrypoint.sh internal/scaffold/assets/entrypoint.sh
```

- [ ] **Step 4: Copy Dockerfile into assets and adjust the entrypoint COPY line**

```bash
cp Dockerfile internal/scaffold/assets/Dockerfile
```

Then edit `internal/scaffold/assets/Dockerfile` and change the single line:

Before:
```dockerfile
COPY entrypoint.sh /etc/workstation-startup.d/200_claude-setup.sh
```

After:
```dockerfile
COPY .ws/entrypoint.sh /etc/workstation-startup.d/200_claude-setup.sh
```

Leave every other line in the Dockerfile identical.

- [ ] **Step 5: Add the go:embed directives**

Create `internal/scaffold/embedded.go`:

```go
// Package scaffold — embedded.go
// Canonical default build assets, compiled into the ws binary.
package scaffold

import _ "embed"

//go:embed assets/Dockerfile
var embeddedDockerfile []byte

//go:embed assets/entrypoint.sh
var embeddedEntrypoint []byte
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/scaffold/ -run TestEmbedded -v`
Expected: PASS (4 tests).

- [ ] **Step 7: Verify full package still builds and all existing tests still pass**

Run: `go test ./...`
Expected: all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/scaffold/assets/Dockerfile internal/scaffold/assets/entrypoint.sh \
        internal/scaffold/embedded.go internal/scaffold/embedded_test.go
git commit -m "feat(scaffold): embed default Dockerfile and entrypoint as build assets"
```

---

## Task 2: Implement ResolveSource (user-template vs embedded precedence)

**Files:**
- Create: `internal/scaffold/source.go`
- Test: `internal/scaffold/source_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/scaffold/source_test.go`:

```go
package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSource_EmbeddedWhenNoUserTemplate(t *testing.T) {
	home := t.TempDir()
	body, label, err := ResolveSource(home, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !bytes.Equal(body, embeddedDockerfile) {
		t.Errorf("expected embedded bytes, got different content (%d vs %d bytes)", len(body), len(embeddedDockerfile))
	}
	if label != "embedded-default" {
		t.Errorf("label = %q, want %q", label, "embedded-default")
	}
}

func TestResolveSource_UserTemplateTakesPrecedence(t *testing.T) {
	home := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("FROM my/custom:latest\n")
	if err := os.WriteFile(filepath.Join(tmplDir, "Dockerfile"), custom, 0644); err != nil {
		t.Fatal(err)
	}

	body, label, err := ResolveSource(home, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !bytes.Equal(body, custom) {
		t.Errorf("expected user-template bytes, got: %q", body)
	}
	if label != "user-template" {
		t.Errorf("label = %q, want %q", label, "user-template")
	}
}

func TestResolveSource_PartialTemplate_FallsBackPerFile(t *testing.T) {
	home := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Only Dockerfile present; entrypoint.sh should fall back.
	if err := os.WriteFile(filepath.Join(tmplDir, "Dockerfile"), []byte("FROM custom\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, dockerLabel, err := ResolveSource(home, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if dockerLabel != "user-template" {
		t.Errorf("Dockerfile label = %q, want user-template", dockerLabel)
	}

	_, entLabel, err := ResolveSource(home, "entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	if entLabel != "embedded-default" {
		t.Errorf("entrypoint.sh label = %q, want embedded-default", entLabel)
	}
}

func TestResolveSource_UnknownName(t *testing.T) {
	home := t.TempDir()
	_, _, err := ResolveSource(home, "unknown-asset")
	if err == nil {
		t.Fatal("expected error for unknown asset name, got nil")
	}
}

func TestResolveSource_UserTemplateUnreadable_Propagates(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file mode permissions")
	}
	home := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmplDir, "Dockerfile")
	if err := os.WriteFile(path, []byte("x"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })

	_, _, err := ResolveSource(home, "Dockerfile")
	if err == nil {
		t.Fatal("expected permission error, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestResolveSource -v`
Expected: FAIL — `undefined: ResolveSource` (package does not compile).

- [ ] **Step 3: Write the implementation**

Create `internal/scaffold/source.go`:

```go
// Package scaffold — source.go
// Resolve the source bytes for a given build asset, honoring user-global
// template overrides at ~/.config/ws/template/<name>.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AssetNames enumerates the build assets ws knows how to scaffold.
var AssetNames = []string{"Dockerfile", "entrypoint.sh"}

// ResolveSource returns the bytes to scaffold for the named asset.
//
// Precedence (per asset, independent):
//  1. ~/.config/ws/template/<name> if present and readable.
//  2. Embedded default compiled into the binary.
//
// Returns (bytes, label, error) where label is "user-template" or
// "embedded-default". Unknown names return an error.
func ResolveSource(home, name string) ([]byte, string, error) {
	embedded, ok := embeddedFor(name)
	if !ok {
		return nil, "", fmt.Errorf("unknown scaffold asset %q (known: %v)", name, AssetNames)
	}

	userPath := filepath.Join(home, ".config", "ws", "template", name)
	data, err := os.ReadFile(userPath)
	if err == nil {
		return data, "user-template", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("reading user template %s: %w", userPath, err)
	}
	return embedded, "embedded-default", nil
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scaffold/ -run TestResolveSource -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/source.go internal/scaffold/source_test.go
git commit -m "feat(scaffold): ResolveSource picks user-template or embedded default"
```

---

## Task 3: Implement ScaffoldAssets (atomic per-file writes with modes)

**Files:**
- Create: `internal/scaffold/scaffold_assets.go`
- Test: `internal/scaffold/scaffold_assets_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/scaffold/scaffold_assets_test.go`:

```go
package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldAssets_WritesBothFromEmbedded(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	var out bytes.Buffer

	res, err := ScaffoldAssets(ScaffoldAssetsOptions{
		Cwd: cwd, Home: home, Stdout: &out,
	})
	if err != nil {
		t.Fatalf("ScaffoldAssets: %v", err)
	}

	dockerPath := filepath.Join(cwd, ".ws", "Dockerfile")
	entPath := filepath.Join(cwd, ".ws", "entrypoint.sh")

	for _, p := range []string{dockerPath, entPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}

	info, _ := os.Stat(dockerPath)
	if info.Mode().Perm() != 0644 {
		t.Errorf("Dockerfile mode = %o, want 0644", info.Mode().Perm())
	}
	info, _ = os.Stat(entPath)
	if info.Mode().Perm() != 0755 {
		t.Errorf("entrypoint.sh mode = %o, want 0755", info.Mode().Perm())
	}

	if !res.Wrote("Dockerfile") || !res.Wrote("entrypoint.sh") {
		t.Errorf("result should record both files written: %+v", res)
	}
	if res.Source("Dockerfile") != "embedded-default" {
		t.Errorf("Dockerfile source = %q, want embedded-default", res.Source("Dockerfile"))
	}
}

func TestScaffoldAssets_RefusesExistingWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(cwd, ".ws", "Dockerfile")
	if err := os.WriteFile(existing, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	_, err := ScaffoldAssets(ScaffoldAssetsOptions{
		Cwd: cwd, Home: home, Stdout: &out,
	})
	if err == nil {
		t.Fatal("expected error for existing file without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want contains 'already exists'", err.Error())
	}

	data, _ := os.ReadFile(existing)
	if string(data) != "old\n" {
		t.Errorf("existing file was modified: %q", string(data))
	}
}

func TestScaffoldAssets_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cwd, ".ws", "Dockerfile")
	if err := os.WriteFile(path, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
		Cwd: cwd, Home: home, Stdout: &out, Force: true,
	}); err != nil {
		t.Fatalf("ScaffoldAssets with --force: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) == "old\n" {
		t.Errorf("expected overwrite, got original content")
	}
}

func TestScaffoldAssets_DryRun_WritesNothing(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	var out bytes.Buffer
	if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
		Cwd: cwd, Home: home, Stdout: &out, DryRun: true,
	}); err != nil {
		t.Fatalf("ScaffoldAssets dry-run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(cwd, ".ws", "Dockerfile")); !os.IsNotExist(err) {
		t.Errorf(".ws/Dockerfile should not exist after dry-run, err=%v", err)
	}
	if !strings.Contains(out.String(), "would write") {
		t.Errorf("expected dry-run summary on stdout, got: %q", out.String())
	}
}

func TestScaffoldAssets_UserTemplatePrecedence(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	customDocker := []byte("FROM my/custom:latest\n")
	if err := os.WriteFile(filepath.Join(tmplDir, "Dockerfile"), customDocker, 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res, err := ScaffoldAssets(ScaffoldAssetsOptions{
		Cwd: cwd, Home: home, Stdout: &out,
	})
	if err != nil {
		t.Fatalf("ScaffoldAssets: %v", err)
	}

	dockerContents, _ := os.ReadFile(filepath.Join(cwd, ".ws", "Dockerfile"))
	if !bytes.Equal(dockerContents, customDocker) {
		t.Errorf("Dockerfile bytes = %q, want user-template bytes", dockerContents)
	}
	if res.Source("Dockerfile") != "user-template" {
		t.Errorf("Dockerfile source = %q, want user-template", res.Source("Dockerfile"))
	}
	if res.Source("entrypoint.sh") != "embedded-default" {
		t.Errorf("entrypoint.sh source = %q, want embedded-default", res.Source("entrypoint.sh"))
	}
}

func TestScaffoldAssets_PartialExisting_OnlyScaffoldsMissing(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	wsDir := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// User already has a custom Dockerfile; entrypoint.sh missing.
	if err := os.WriteFile(filepath.Join(wsDir, "Dockerfile"), []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res, err := ScaffoldAssets(ScaffoldAssetsOptions{
		Cwd: cwd, Home: home, Stdout: &out, OnlyMissing: true,
	})
	if err != nil {
		t.Fatalf("ScaffoldAssets OnlyMissing: %v", err)
	}
	if res.Wrote("Dockerfile") {
		t.Errorf("should not overwrite existing Dockerfile when OnlyMissing=true")
	}
	if !res.Wrote("entrypoint.sh") {
		t.Errorf("should write missing entrypoint.sh when OnlyMissing=true")
	}

	data, _ := os.ReadFile(filepath.Join(wsDir, "Dockerfile"))
	if string(data) != "USER\n" {
		t.Errorf("existing Dockerfile modified under OnlyMissing, got %q", data)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/ -run TestScaffoldAssets -v`
Expected: FAIL — `undefined: ScaffoldAssets` / `undefined: ScaffoldAssetsOptions`.

- [ ] **Step 3: Write the implementation**

Create `internal/scaffold/scaffold_assets.go`:

```go
// Package scaffold — scaffold_assets.go
// Materialize the default Dockerfile and entrypoint.sh into <project>/.ws/.
package scaffold

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type ScaffoldAssetsOptions struct {
	Cwd    string    // project root
	Home   string    // user home (for ~/.config/ws/template/ lookup)
	Stdout io.Writer // log target

	Force       bool // overwrite existing files
	DryRun      bool // print what would be written, touch nothing
	OnlyMissing bool // skip files that already exist without erroring
}

// AssetResult records, per asset name, whether the file was (or would be)
// written and which source provided its bytes.
type AssetResult struct {
	wrote  map[string]bool
	source map[string]string
}

func (r AssetResult) Wrote(name string) bool   { return r.wrote[name] }
func (r AssetResult) Source(name string) string { return r.source[name] }

// assetMode returns the file mode for a named asset.
func assetMode(name string) os.FileMode {
	if name == "entrypoint.sh" {
		return 0755
	}
	return 0644
}

// ScaffoldAssets writes each asset in AssetNames to <Cwd>/.ws/<name>.
func ScaffoldAssets(opts ScaffoldAssetsOptions) (AssetResult, error) {
	res := AssetResult{
		wrote:  map[string]bool{},
		source: map[string]string{},
	}

	wsDir := filepath.Join(opts.Cwd, ".ws")
	if !opts.DryRun {
		if err := os.MkdirAll(wsDir, 0755); err != nil {
			return res, fmt.Errorf("creating %s: %w", wsDir, err)
		}
	}

	for _, name := range AssetNames {
		target := filepath.Join(wsDir, name)

		_, statErr := os.Stat(target)
		exists := statErr == nil
		if !exists && !errors.Is(statErr, os.ErrNotExist) {
			return res, fmt.Errorf("stat %s: %w", target, statErr)
		}

		if exists {
			switch {
			case opts.OnlyMissing:
				res.source[name] = "preexisting"
				fmt.Fprintf(opts.Stdout, "skip .ws/%s (already present)\n", name)
				continue
			case !opts.Force:
				return res, fmt.Errorf("%s already exists; re-run with --force to overwrite", target)
			}
		}

		body, source, err := ResolveSource(opts.Home, name)
		if err != nil {
			return res, err
		}
		res.source[name] = source

		if opts.DryRun {
			fmt.Fprintf(opts.Stdout, "would write .ws/%s (%d bytes, from %s)\n", name, len(body), source)
			continue
		}

		if err := writeFileAtomic(target, body, assetMode(name)); err != nil {
			return res, err
		}
		res.wrote[name] = true
		fmt.Fprintf(opts.Stdout, "wrote .ws/%s (from %s)\n", name, source)
	}

	return res, nil
}

// writeFileAtomic writes body to path with mode, via a temp file + rename
// in the same directory so partial writes cannot corrupt an existing file.
func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ws-asset-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming to %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scaffold/ -run TestScaffoldAssets -v`
Expected: PASS (6 tests).

- [ ] **Step 5: Run the full scaffold package tests**

Run: `go test ./internal/scaffold/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/scaffold_assets.go internal/scaffold/scaffold_assets_test.go
git commit -m "feat(scaffold): write Dockerfile and entrypoint.sh into .ws/ atomically"
```

---

## Task 4: Migrate per-project config path from .ws.yaml to .ws/config.yaml

**Files:**
- Create: `internal/config/migrate.go`
- Test: `internal/config/migrate_test.go`
- Modify: `internal/config/discover.go`
- Modify: `internal/config/discover_test.go`
- Modify: `internal/scaffold/scaffold.go` (path the project config is written to)
- Modify: `internal/scaffold/templates.go` (commented header text)
- Modify: `internal/scaffold/scaffold_test.go`, `internal/scaffold/templates_test.go`
- Modify: `cmd/setup.go` (flag descriptions)
- Modify: `cmd/setup_test.go` (path assertions)

- [ ] **Step 1: Write failing tests for MigrateLegacyProjectConfig**

Create `internal/config/migrate_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestMigrateLegacy_MovesFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".ws.yaml")
	if err := os.WriteFile(legacy, []byte("project_id: foo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	migrated, err := config.MigrateLegacyProjectConfig(dir)
	if err != nil {
		t.Fatalf("MigrateLegacyProjectConfig: %v", err)
	}
	if !migrated {
		t.Fatal("expected migrated=true")
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, err=%v", legacy, err)
	}
	newPath := filepath.Join(dir, ".ws", "config.yaml")
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("reading %s: %v", newPath, err)
	}
	if string(data) != "project_id: foo\n" {
		t.Errorf("content mismatch after migration: %q", data)
	}
}

func TestMigrateLegacy_NoLegacy_NoOp(t *testing.T) {
	dir := t.TempDir()
	migrated, err := config.MigrateLegacyProjectConfig(dir)
	if err != nil {
		t.Fatalf("MigrateLegacyProjectConfig: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false when no legacy file present")
	}
}

func TestMigrateLegacy_BothPresent_Errors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ws.yaml"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ws", "config.yaml"), []byte("y\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := config.MigrateLegacyProjectConfig(dir)
	if err == nil {
		t.Fatal("expected error when both files present")
	}
}

func TestMigrateLegacy_CreatesWsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ws.yaml"), []byte("z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.MigrateLegacyProjectConfig(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".ws"))
	if err != nil {
		t.Fatalf("stat .ws: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected .ws to be a directory")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestMigrateLegacy -v`
Expected: FAIL — `undefined: config.MigrateLegacyProjectConfig`.

- [ ] **Step 3: Implement MigrateLegacyProjectConfig**

Create `internal/config/migrate.go`:

```go
// ws/internal/config/migrate.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MigrateLegacyProjectConfig moves <dir>/.ws.yaml to <dir>/.ws/config.yaml
// when only the legacy file is present. Returns (migrated, err):
//
//   - (true, nil)  — migration happened
//   - (false, nil) — no legacy file to migrate
//   - (false, err) — both files present (ambiguous), or the move failed
//
// The move is atomic via os.Rename on the same filesystem; .ws/ is created
// at 0755 if missing.
func MigrateLegacyProjectConfig(dir string) (bool, error) {
	legacy := filepath.Join(dir, ".ws.yaml")
	newPath := filepath.Join(dir, ".ws", "config.yaml")

	_, legacyErr := os.Stat(legacy)
	legacyExists := legacyErr == nil
	if !legacyExists && !errors.Is(legacyErr, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", legacy, legacyErr)
	}
	if !legacyExists {
		return false, nil
	}

	if _, err := os.Stat(newPath); err == nil {
		return false, fmt.Errorf("conflicting config: %s and %s both present — remove %s",
			newPath, legacy, legacy)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", newPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(newPath), err)
	}
	if err := os.Rename(legacy, newPath); err != nil {
		return false, fmt.Errorf("moving %s to %s: %w", legacy, newPath, err)
	}
	return true, nil
}
```

- [ ] **Step 4: Run migrate tests to verify pass**

Run: `go test ./internal/config/ -run TestMigrateLegacy -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Write failing tests for updated FindProjectConfig**

Replace the body of `internal/config/discover_test.go` with:

```go
// ws/internal/config/discover_test.go
package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".ws", "config.yaml")
	if err := os.WriteFile(p, []byte("project_id: test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindProjectConfig_FoundInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	want := writeConfig(t, dir)
	got, err := config.FindProjectConfig(dir, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindProjectConfig_FoundInParent(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	want := writeConfig(t, root)
	got, err := config.FindProjectConfig(sub, filepath.Dir(root))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindProjectConfig_StopsAtHome(t *testing.T) {
	home := t.TempDir()
	sub := filepath.Join(home, "project", "src")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := config.FindProjectConfig(sub, home)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindProjectConfig_NotFoundReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := config.FindProjectConfig(dir, filepath.Dir(dir))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindProjectConfig_AutoMigratesLegacy(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".ws.yaml")
	if err := os.WriteFile(legacy, []byte("project_id: legacy\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := config.FindProjectConfig(dir, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, ".ws", "config.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("expected %s removed after migration", legacy)
	}
}

func TestFindProjectConfig_BothPresent_Errors(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir) // writes .ws/config.yaml
	if err := os.WriteFile(filepath.Join(dir, ".ws.yaml"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := config.FindProjectConfig(dir, dir)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	fmt.Fprintln(os.Stderr, err)
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestFindProjectConfig -v`
Expected: FAIL — current `FindProjectConfig` still looks for `.ws.yaml`.

- [ ] **Step 7: Update FindProjectConfig**

Replace `internal/config/discover.go` with:

```go
// ws/internal/config/discover.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindProjectConfig walks up from startDir looking for .ws/config.yaml,
// stopping at stopDir or the filesystem root. At each level, if only a
// legacy .ws.yaml is present, it is auto-migrated to .ws/config.yaml.
// If both exist at the same level, returns an error (ambiguous).
func FindProjectConfig(startDir, stopDir string) (string, error) {
	dir := startDir
	for {
		newPath := filepath.Join(dir, ".ws", "config.yaml")
		if _, err := os.Stat(newPath); err == nil {
			// New path present — check the legacy path isn't also present.
			if _, err := os.Stat(filepath.Join(dir, ".ws.yaml")); err == nil {
				return "", fmt.Errorf(
					"conflicting config: %s and %s both present — remove %s",
					newPath, filepath.Join(dir, ".ws.yaml"), filepath.Join(dir, ".ws.yaml"))
			}
			return newPath, nil
		}

		migrated, err := MigrateLegacyProjectConfig(dir)
		if err != nil {
			return "", err
		}
		if migrated {
			fmt.Fprintf(os.Stderr,
				"note: migrated %s → %s\n",
				filepath.Join(dir, ".ws.yaml"), newPath)
			return newPath, nil
		}

		if dir == stopDir || dir == "/" {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no .ws/config.yaml found; create one with 'ws setup' in your project root")
}
```

- [ ] **Step 8: Run config tests to verify pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (all tests including old ones).

- [ ] **Step 9: Update scaffold output path and template comments**

In `internal/scaffold/scaffold.go`, find the two lines that write the project config and change the path. Replace:

```go
if writeProject {
    fmt.Fprintln(opts.Stdout, "About to write "+filepath.Join(opts.Cwd, ".ws.yaml")+":")
```

with:

```go
if writeProject {
    fmt.Fprintln(opts.Stdout, "About to write "+filepath.Join(opts.Cwd, ".ws", "config.yaml")+":")
```

And replace:

```go
if writeProject {
    if err := WriteAtomic(filepath.Join(opts.Cwd, ".ws.yaml"), []byte(project.marshal()), opts.Force); err != nil {
        return err
    }
}
```

with:

```go
if writeProject {
    if err := WriteAtomic(filepath.Join(opts.Cwd, ".ws", "config.yaml"), []byte(project.marshal()), opts.Force); err != nil {
        return err
    }
}
```

In `TemplateScaffold`, find:

```go
if !opts.GlobalOnly {
    path := filepath.Join(opts.Cwd, ".ws.yaml")
```

and change to:

```go
if !opts.GlobalOnly {
    path := filepath.Join(opts.Cwd, ".ws", "config.yaml")
```

- [ ] **Step 10: Update the template comment header**

In `internal/scaffold/templates.go`, find the project template constant and change the first comment line from:

```
# ws project config — committed to the repo root
```

to:

```
# ws project config (.ws/config.yaml) — committed to git
```

If `templates_test.go` asserts on the old text, update the expected substring accordingly.

- [ ] **Step 11: Update cmd/setup.go flag descriptions**

In `cmd/setup.go`, change:

```go
c.Flags().Bool("project-only", false, "scaffold only .ws.yaml")
```

to:

```go
c.Flags().Bool("project-only", false, "scaffold only .ws/config.yaml")
```

Long description at `setupCmd.Long` — change `.ws.yaml` → `.ws/config.yaml`.

- [ ] **Step 12: Update cmd/setup_test.go path assertions**

In `cmd/setup_test.go`, replace every occurrence of `".ws.yaml"` with `filepath.Join(".ws", "config.yaml")` for absolute paths under `cwd`. Concretely:

Old:
```go
projectPath := filepath.Join(cwd, ".ws.yaml")
```
New:
```go
projectPath := filepath.Join(cwd, ".ws", "config.yaml")
```

Also any pre-write of a stub:
Old:
```go
if err := os.WriteFile(filepath.Join(cwd, ".ws.yaml"), []byte("old\n"), 0644); err != nil {
```
New:
```go
if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
    t.Fatal(err)
}
if err := os.WriteFile(filepath.Join(cwd, ".ws", "config.yaml"), []byte("old\n"), 0644); err != nil {
```

And update the "already exists" error substring if tests check for it — the `WriteAtomic` error message uses the full path, which now ends in `.ws/config.yaml`.

- [ ] **Step 13: Update internal/scaffold/scaffold_test.go**

Any `.ws.yaml` reference in assertions → `.ws/config.yaml`. Any pre-created stub paths updated the same way as above.

- [ ] **Step 14: Run every test**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 15: Commit**

```bash
git add internal/config/migrate.go internal/config/migrate_test.go \
        internal/config/discover.go internal/config/discover_test.go \
        internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go \
        internal/scaffold/templates.go internal/scaffold/templates_test.go \
        cmd/setup.go cmd/setup_test.go
git commit -m "feat(config): move per-project config to .ws/config.yaml with auto-migration"
```

---

## Task 5: Add `ws setup scaffold` command

**Files:**
- Create: `cmd/scaffold.go`
- Create: `cmd/scaffold_test.go`

- [ ] **Step 1: Write failing tests**

Create `cmd/scaffold_test.go`:

```go
// ws/cmd/scaffold_test.go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cmd"
)

func runSetupScaffold(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr,
		append([]string{"setup", "scaffold"}, args...))
	return code, stdout.String(), stderr.String()
}

func TestSetupScaffold_WritesEmbeddedDefaults(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		p := filepath.Join(cwd, ".ws", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestSetupScaffold_RefusesExistingWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ws", "Dockerfile"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold"})
	if code == 0 {
		t.Fatalf("expected non-zero exit, stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("stderr should mention 'already exists': %q", stderr.String())
	}
}

func TestSetupScaffold_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	if err := os.MkdirAll(filepath.Join(cwd, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ws", "Dockerfile"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold", "--force"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}

	data, _ := os.ReadFile(filepath.Join(cwd, ".ws", "Dockerfile"))
	if string(data) == "old\n" {
		t.Errorf("--force did not overwrite")
	}
}

func TestSetupScaffold_DryRun_WritesNothing(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "scaffold", "--dry-run"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(cwd, ".ws", "Dockerfile")); !os.IsNotExist(err) {
		t.Error("dry-run should not create .ws/Dockerfile")
	}
	if !strings.Contains(stdout.String(), "would write") {
		t.Errorf("expected 'would write' in stdout, got %q", stdout.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/ -run TestSetupScaffold -v`
Expected: FAIL — subcommand `scaffold` unknown; command errors with `unknown command "scaffold" for "ws setup"`.

- [ ] **Step 3: Implement cmd/scaffold.go**

Create `cmd/scaffold.go`:

```go
// ws/cmd/scaffold.go
package cmd

import (
	"fmt"
	"os"

	"github.com/adamtait/remote-agent/internal/scaffold"
	"github.com/spf13/cobra"
)

var setupScaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Write customizable .ws/Dockerfile and .ws/entrypoint.sh into the project",
	Long: `Materialize the default build assets (Dockerfile, entrypoint.sh) into
<project>/.ws/. Users (and AI agents) edit these files freely; ws does not
modify them on upgrade. Re-run with --force to overwrite with current
embedded defaults.

Source precedence per file: ~/.config/ws/template/<name> if present, else
the embedded default compiled into the ws binary.`,
	// Must run in fresh repos that have no .ws/config.yaml yet.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		f := readFlags(cmd, "force", "dry-run")
		_, err = scaffold.ScaffoldAssets(scaffold.ScaffoldAssetsOptions{
			Cwd: cwd, Home: home, Stdout: cmd.OutOrStdout(),
			Force:  f["force"],
			DryRun: f["dry-run"],
		})
		return err
	},
}

func init() {
	setupScaffoldCmd.Flags().Bool("force", false, "overwrite existing .ws/* files")
	setupScaffoldCmd.Flags().Bool("dry-run", false, "print what would be written without touching disk")
	setupCmd.AddCommand(setupScaffoldCmd)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/ -run TestSetupScaffold -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Full build and test sweep**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/scaffold.go cmd/scaffold_test.go
git commit -m "feat(cmd): add 'ws setup scaffold' for .ws/ build asset materialization"
```

---

## Task 6: Extend `ws setup template` to write .ws/ assets

**Files:**
- Modify: `internal/scaffold/scaffold.go` (TemplateScaffold body)
- Modify: `cmd/setup_test.go` (extend)

- [ ] **Step 1: Write failing test**

Append to `cmd/setup_test.go`:

```go
func TestSetupTemplate_WritesAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		p := filepath.Join(cwd, ".ws", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestSetupTemplate_GlobalOnly_SkipsAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template", "--global-only"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, ".ws", "Dockerfile")); !os.IsNotExist(err) {
		t.Error("--global-only should not write .ws/Dockerfile")
	}
}

func TestSetupTemplate_ProjectOnly_WritesAssets(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdin, stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup", "template", "--project-only"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ws", name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// Global config should not exist under --project-only.
	if _, err := os.Stat(filepath.Join(home, ".config", "ws", "config.yaml")); !os.IsNotExist(err) {
		t.Error("--project-only should not write ~/.config/ws/config.yaml")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/ -run "TestSetupTemplate_WritesAssets|TestSetupTemplate_GlobalOnly_SkipsAssets|TestSetupTemplate_ProjectOnly_WritesAssets" -v`
Expected: FAIL — `.ws/Dockerfile` is not written by `TemplateScaffold` today.

- [ ] **Step 3: Extend TemplateScaffold to write assets**

In `internal/scaffold/scaffold.go`, find the `TemplateScaffold` function. At the bottom of its body (after the existing writes but before `return nil`), insert:

```go
	if !opts.GlobalOnly {
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			Cwd: opts.Cwd, Home: opts.Home, Stdout: opts.Stdout,
			Force: opts.Force,
		}); err != nil {
			return err
		}
	}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./cmd/ -v`
Expected: PASS (all tests, including the three new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/scaffold.go cmd/setup_test.go
git commit -m "feat(setup): 'ws setup template' also scaffolds .ws/ build assets"
```

---

## Task 7: Prompt for asset scaffolding at end of interactive `ws setup`

**Files:**
- Modify: `internal/scaffold/scaffold.go` (InteractiveScaffold tail)
- Modify: `cmd/setup_test.go`

- [ ] **Step 1: Write failing test**

Append to `cmd/setup_test.go`:

```go
func TestSetupInteractive_ScaffoldPromptYes(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	// Pipe pre-canned answers for every prompt in order:
	//   project_id, github_repo_url, region, git_user_name, git_user_email,
	//   Proceed? [Y/n], Scaffold? [Y/n]
	input := strings.Join([]string{
		"my-project",                 // project_id
		"https://github.com/a/b.git", // github_repo_url
		"us-central1",                // region (global defaults)
		"Alice",                      // git_user_name
		"a@example.com",              // git_user_email
		"Y",                          // Proceed?
		"Y",                          // Scaffold .ws/ assets?
		"",                           // trailing newline
	}, "\n")
	var stdin bytes.Buffer
	stdin.WriteString(input)

	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ws", name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestSetupInteractive_ScaffoldPromptNo(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	input := strings.Join([]string{
		"my-project",
		"https://github.com/a/b.git",
		"us-central1",
		"Alice",
		"a@example.com",
		"Y", // Proceed
		"n", // Scaffold? NO
		"",
	}, "\n")
	var stdin bytes.Buffer
	stdin.WriteString(input)

	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&stdin, &stdout, &stderr, []string{"setup"})
	if code != 0 {
		t.Fatalf("exit %d. stderr=%q", code, stderr.String())
	}
	// Config must exist…
	if _, err := os.Stat(filepath.Join(cwd, ".ws", "config.yaml")); err != nil {
		t.Errorf("config.yaml should exist: %v", err)
	}
	// …but assets must NOT.
	if _, err := os.Stat(filepath.Join(cwd, ".ws", "Dockerfile")); !os.IsNotExist(err) {
		t.Error("Dockerfile should not be scaffolded when user answered 'n'")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/ -run TestSetupInteractive_ScaffoldPrompt -v`
Expected: FAIL — no scaffold prompt today; `n` consumed as something else or treated as extra input.

- [ ] **Step 3: Implement the prompt**

In `internal/scaffold/scaffold.go`, near the very end of `InteractiveScaffold` (after both config files have been written and before the `Run 'ws create'…` line), insert:

```go
	// Scaffold prompt — only when project assets would apply.
	if writeProject {
		answer, err := Prompt(stdin, opts.Stdout,
			"Scaffold customizable .ws/Dockerfile and .ws/entrypoint.sh? [Y/n]",
			"Y", nil)
		if err != nil {
			return err
		}
		ans := strings.ToLower(strings.TrimSpace(answer))
		if ans != "n" && ans != "no" {
			if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
				Cwd: opts.Cwd, Home: opts.Home, Stdout: opts.Stdout,
				Force: opts.Force,
			}); err != nil {
				return err
			}
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/scaffold.go cmd/setup_test.go
git commit -m "feat(setup): prompt to scaffold .ws/ assets in interactive setup"
```

---

## Task 8: Preflight + build args in `ws create`

**Files:**
- Modify: `cmd/create.go`
- Create: `cmd/create_test.go`

- [ ] **Step 1: Inspect current create.go preflight location**

Run: `grep -n "docker" cmd/create.go`
Expected: shows the `buildx build` line (around line 142 in the current repo).

The insertion point: immediately before the existing
`if err := r.Run("auth", "configure-docker", ...)` block
(around line 139 in the current repo — "Step 3 — Build and push image").

- [ ] **Step 2: Write failing tests**

Create `cmd/create_test.go`:

```go
// ws/cmd/create_test.go
package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDockerRunner records args and optionally fails.
type fakeDockerRunner struct {
	runs [][]string
	err  error
}

func (f *fakeDockerRunner) Run(args ...string) error {
	f.runs = append(f.runs, args)
	return f.err
}

// Preflight tests exercise ensureBuildAssets, which we expose in cmd to
// test without running the whole create flow.
func TestEnsureBuildAssets_Present_ReturnsNil(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		if err := os.WriteFile(filepath.Join(ws, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var stdin bytes.Buffer
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, cwd, &stdin, &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureBuildAssets_MissingNonTTY_Errors(t *testing.T) {
	cwd := t.TempDir()
	var stdin bytes.Buffer // not a TTY
	var out bytes.Buffer
	err := ensureBuildAssets(cwd, t.TempDir(), &stdin, &out, false)
	if err == nil {
		t.Fatal("expected error when missing + non-TTY")
	}
	if !strings.Contains(err.Error(), "ws setup scaffold") {
		t.Errorf("error should mention 'ws setup scaffold', got: %v", err)
	}
}

func TestEnsureBuildAssets_MissingTTYAnswerYes_Scaffolds(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := bytes.NewBufferString("Y\n")
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, home, stdin, &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		p := filepath.Join(cwd, ".ws", n)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist after prompt=yes: %v", p, err)
		}
	}
}

func TestEnsureBuildAssets_MissingTTYAnswerNo_Errors(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := bytes.NewBufferString("n\n")
	var out bytes.Buffer
	err := ensureBuildAssets(cwd, home, stdin, &out, true)
	if err == nil {
		t.Fatal("expected error when user answered 'n'")
	}
}

func TestEnsureBuildAssets_PartialExistingTTYYes_ScaffoldsOnlyMissing(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "Dockerfile"), []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stdin := bytes.NewBufferString("Y\n")
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, t.TempDir(), stdin, &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "Dockerfile"))
	if string(data) != "USER\n" {
		t.Errorf("existing Dockerfile should not be overwritten, got %q", data)
	}
	if _, err := os.Stat(filepath.Join(ws, "entrypoint.sh")); err != nil {
		t.Errorf("missing entrypoint.sh should have been scaffolded: %v", err)
	}
}

func TestBuildArgs_UsesDashFAndProjectRootContext(t *testing.T) {
	got := dockerBuildArgs("example.com/img:tag")
	want := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", "example.com/img:tag",
		"-f", ".ws/Dockerfile",
		"--push",
		".",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("args mismatch.\n got=%v\nwant=%v", got, want)
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./cmd/ -run "TestEnsureBuildAssets|TestBuildArgs" -v`
Expected: FAIL — `undefined: ensureBuildAssets` and `undefined: dockerBuildArgs`.

- [ ] **Step 4: Add ensureBuildAssets and dockerBuildArgs to cmd/create.go**

At the top of `cmd/create.go` (after the existing imports), ensure `io` and the scaffold package are imported:

```go
import (
    "bufio"
    "bytes"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"

    "github.com/adamtait/remote-agent/internal/gcloud"
    "github.com/adamtait/remote-agent/internal/scaffold"
    "github.com/spf13/cobra"
    "golang.org/x/term"
)
```

At the bottom of `cmd/create.go`, append:

```go
// ensureBuildAssets verifies .ws/Dockerfile and .ws/entrypoint.sh exist in
// cwd. If missing and stdin is a TTY, prompts to scaffold. If missing and
// non-TTY, returns an error directing the user to run 'ws setup scaffold'.
func ensureBuildAssets(cwd, home string, stdin io.Reader, out io.Writer, isTTY bool) error {
	missing := []string{}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ws", n)); err != nil {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !isTTY {
		return fmt.Errorf(".ws/ not initialized (missing: %s) — run 'ws setup scaffold' first",
			strings.Join(missing, ", "))
	}

	reader := bufio.NewReader(stdin)
	fmt.Fprintf(out, "No .ws/ assets (missing: %s). Scaffold defaults now? [Y/n] ",
		strings.Join(missing, ", "))
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading stdin: %w", err)
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans == "n" || ans == "no" {
		return fmt.Errorf(".ws/ not initialized — re-run 'ws create' after 'ws setup scaffold'")
	}

	_, err = scaffold.ScaffoldAssets(scaffold.ScaffoldAssetsOptions{
		Cwd: cwd, Home: home, Stdout: out,
		OnlyMissing: true,
	})
	return err
}

// dockerBuildArgs returns the buildx args used by `ws create`, centralized so
// tests can assert on the exact flags.
func dockerBuildArgs(imageURI string) []string {
	return []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", imageURI,
		"-f", ".ws/Dockerfile",
		"--push",
		".",
	}
}
```

Also add `errors` to imports if not already present.

- [ ] **Step 5: Wire the preflight and new args into runCreate**

In `cmd/create.go`, find the existing lines around the docker build:

```go
// Step 3 — Build and push image
logStep(fmt.Sprintf("Building image %s...", imageURI))
if err := r.Run("auth", "configure-docker", fmt.Sprintf("%s-docker.pkg.dev", cfg.Region), "--quiet"); err != nil {
    return fmt.Errorf("configuring docker auth: %w", err)
}
if err := (dockerRunner{}).Run("buildx", "build", "--platform", "linux/amd64", "-t", imageURI, "--push", "."); err != nil {
    return fmt.Errorf("building and pushing image: %w", err)
}
```

Replace with:

```go
// Step 3 — Build and push image
logStep(fmt.Sprintf("Building image %s...", imageURI))

home, err := os.UserHomeDir()
if err != nil {
    return fmt.Errorf("determining home directory: %w", err)
}
cwd, err := os.Getwd()
if err != nil {
    return fmt.Errorf("determining working directory: %w", err)
}
isTTY := term.IsTerminal(int(os.Stdin.Fd()))
if err := ensureBuildAssets(cwd, home, os.Stdin, os.Stdout, isTTY); err != nil {
    return err
}

if err := r.Run("auth", "configure-docker", fmt.Sprintf("%s-docker.pkg.dev", cfg.Region), "--quiet"); err != nil {
    return fmt.Errorf("configuring docker auth: %w", err)
}
if err := (dockerRunner{}).Run(dockerBuildArgs(imageURI)...); err != nil {
    return fmt.Errorf("building and pushing image: %w", err)
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./cmd/ -run "TestEnsureBuildAssets|TestBuildArgs" -v`
Expected: PASS.

- [ ] **Step 7: Full build and test sweep**

Run: `go build ./... && go test ./...`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/create.go cmd/create_test.go
git commit -m "feat(create): preflight .ws/ assets and build with -f .ws/Dockerfile"
```

---

## Task 9: Dogfood migration — remove top-level Dockerfile/entrypoint.sh, scaffold this repo's `.ws/`

**Files:**
- Delete: `Dockerfile`
- Delete: `entrypoint.sh`
- Create: `.ws/Dockerfile` (via built `ws setup scaffold`)
- Create: `.ws/entrypoint.sh` (via built `ws setup scaffold`)
- Move: `.ws.yaml` → `.ws/config.yaml` (handled by auto-migration on next `ws` invocation)

- [ ] **Step 1: Rebuild the ws binary with all the new code**

Run:
```bash
go build -o /tmp/ws-bin ./
```
Expected: build succeeds.

- [ ] **Step 2: Remove the top-level Dockerfile and entrypoint.sh**

Run:
```bash
rm Dockerfile entrypoint.sh
```

- [ ] **Step 3: Scaffold .ws/ from the embedded defaults**

Run the just-built binary in this repo:
```bash
/tmp/ws-bin setup scaffold
```
Expected stdout contains `wrote .ws/Dockerfile (from embedded-default)` and `wrote .ws/entrypoint.sh (from embedded-default)`.

- [ ] **Step 4: Verify .ws.yaml auto-migrated**

Run any command that triggers `PersistentPreRunE`, e.g.:
```bash
/tmp/ws-bin list 2>&1 || true
```
Expected stderr includes `note: migrated .ws.yaml → .ws/config.yaml` (or — if `.ws.yaml` was already removed by hand — no migration notice).

Then check on disk:
```bash
ls .ws/
```
Expected: `Dockerfile`, `config.yaml`, `entrypoint.sh`.

- [ ] **Step 5: Verify scaffolded files match embedded (sanity)**

```bash
diff .ws/Dockerfile internal/scaffold/assets/Dockerfile && \
diff .ws/entrypoint.sh internal/scaffold/assets/entrypoint.sh
```
Expected: no diff output (exit 0).

- [ ] **Step 6: Run every test once more**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit the repo migration**

```bash
git add -A .ws/ Dockerfile entrypoint.sh .ws.yaml
git commit -m "chore(repo): dogfood — move build assets into .ws/"
```

(`git add -A` is used here narrowly for the five files listed above: three new files under `.ws/` and two deletions at the top level. Verify with `git status` before committing.)

---

## Self-review

### Spec coverage

| Spec section / requirement | Task |
|---|---|
| Customization model: full override | Tasks 3, 5 |
| `.ws/` layout (Dockerfile, entrypoint.sh, config.yaml) | Tasks 3, 4, 9 |
| `~/.config/ws/template/` user-global override | Tasks 2, 3 |
| Embedded defaults via `go:embed` | Task 1 |
| `ws setup scaffold` command | Task 5 |
| `ws setup` extended with scaffold prompt | Task 7 |
| `ws setup template` writes `.ws/` assets | Task 6 |
| `ws setup template --global-only` skips assets | Task 6 |
| `ws create` preflight | Task 8 |
| Build context = project root; `-f .ws/Dockerfile` | Task 8 |
| Config discovery: `.ws/config.yaml` | Task 4 |
| Legacy `.ws.yaml` auto-migration | Task 4 |
| Both-present conflict error | Task 4 |
| Dogfood this repo | Task 9 |
| Atomic writes | Task 3 |
| File modes (0644 Dockerfile, 0755 entrypoint.sh) | Task 3 |
| Per-file precedence for user-template | Tasks 2, 3 |
| Error handling table | Covered across task error cases |

### Type consistency

- `ResolveSource` returns `(body []byte, label string, err error)` — same signature across Tasks 2, 3.
- `ScaffoldAssets` takes `ScaffoldAssetsOptions` and returns `(AssetResult, error)` — used consistently in Tasks 3, 5, 6, 7, 8.
- `AssetResult` methods: `Wrote(name) bool`, `Source(name) string` — used in tests in Task 3.
- `AssetNames = []string{"Dockerfile", "entrypoint.sh"}` — referenced in Tasks 2, 3, 8.
- Flag names: `--force`, `--dry-run` consistent across commands.
- `ensureBuildAssets(cwd, home, stdin, out, isTTY)` signature matches call site in Task 8 Step 5.

### Placeholder scan

- No "TBD" / "TODO" / "handle appropriately" / "similar to Task N" strings. Code blocks are concrete. Commands show exact expected output or behavior.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-22-ws-project-scaffolding.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
