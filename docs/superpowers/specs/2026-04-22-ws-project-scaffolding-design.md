# ws project scaffolding — design

**Date:** 2026-04-22
**Status:** Approved

## Motivation

Today `ws` works only when invoked from the workspace repo itself, because `ws
create` runs `docker buildx build .` against the current directory and expects
`Dockerfile` + `entrypoint.sh` to live there. That ties the tool to a single
repo and blocks two goals:

1. **Use `ws` in any git root.** A developer with the `ws` binary installed
   should be able to run `ws create` inside any project and get a workstation
   provisioned for that repo.
2. **Customize the container build and workstation setup.** Humans and AI
   agents should have full control over the Dockerfile (what the image
   contains) and the entrypoint (what runs at container boot).

This design extends `ws` with embedded default build assets and a scaffolding
command so every project gets a fully customizable `.ws/` directory of its own.

## Customization model

Full override with scaffolding. `ws setup scaffold` drops a complete copy of
the default Dockerfile and entrypoint.sh into the project's `.ws/` directory.
Users (and AI agents) edit them freely. There are no hooks, overlays, or
append-only layers. Upgrades to `ws` do not modify the user's files; re-running
`ws setup scaffold --force` overwrites them with current defaults.

## File layout

### In every project repo that uses `ws` (tracked in git)

```
<project-root>/
└── .ws/
    ├── config.yaml       # project config (formerly .ws.yaml)
    ├── Dockerfile        # full copy of the default; user edits freely
    └── entrypoint.sh     # full copy of the default; user edits freely
```

### Per-user, per-machine (optional, not tracked)

```
~/.config/ws/
├── config.yaml           # existing — user-wide defaults + per-project overrides
└── template/             # NEW, optional
    ├── Dockerfile        # personal baseline, used instead of embedded default
    └── entrypoint.sh     # same
```

### Inside the ws source repo (canonical defaults)

```
internal/scaffold/assets/
├── Dockerfile
└── entrypoint.sh
```

These are the single source of truth for the embedded defaults. The repo's own
top-level `Dockerfile` and `entrypoint.sh` are removed; this repo's own `.ws/`
is scaffolded from the binary like any other project.

### Scaffolding precedence (per file, independent)

1. `~/.config/ws/template/<file>` if present → copy bytes
2. Embedded default → copy bytes

Each file is resolved independently. A user who only wants a custom Dockerfile
does not need to also provide an entrypoint.sh template.

## Command surface

### New: `ws setup scaffold`

```
ws setup scaffold            # writes .ws/Dockerfile and .ws/entrypoint.sh
ws setup scaffold --force    # overwrite existing .ws/* assets
ws setup scaffold --dry-run  # print what it would write, don't touch disk
```

- Writes each file atomically (temp file in `.ws/`, then rename).
- Modes: `0644` for `Dockerfile`, `0755` for `entrypoint.sh`.
- Creates `.ws/` at `0755` if missing.
- Logs per file: `wrote .ws/<name> (from <user-template|embedded-default>)`.

### Extended: `ws setup` (existing interactive flow)

After writing `.ws/config.yaml` and `~/.config/ws/config.yaml`, prompts:

```
Scaffold customizable .ws/Dockerfile and .ws/entrypoint.sh? [Y/n]
```

If yes, invokes the same scaffold logic. Leaves a fresh project fully ready
for `ws create` without requiring a second command.

### Extended: `ws setup template`

Writes `.ws/Dockerfile` and `.ws/entrypoint.sh` unconditionally as part of the
non-interactive template output. Honors existing flags:

- `--force` overwrites existing files.
- `--project-only`: writes `.ws/config.yaml` + `.ws/Dockerfile` + `.ws/entrypoint.sh`.
- `--global-only`: skips `.ws/` scaffolding entirely (only touches
  `~/.config/ws/config.yaml`).

### Extended: `ws create`

Before invoking `docker buildx build`, checks that both `.ws/Dockerfile` and
`.ws/entrypoint.sh` exist:

- **TTY stdin:** prompts `No .ws/ assets. Scaffold defaults now? [Y/n]`. Yes
  → scaffold (only the missing files, never overwriting) then continue. No →
  error and exit.
- **Non-TTY stdin (CI):** errors immediately with
  `.ws/ not initialized — run 'ws setup scaffold' first`.

Build command changes:

```
docker buildx build --platform linux/amd64 \
    -t <image-uri> \
    -f .ws/Dockerfile \
    --push \
    .
```

- Build context: project root. Honors `.dockerignore` at the project root.
- Dockerfile explicit via `-f`. Inside the Dockerfile, the entrypoint COPY
  becomes `COPY .ws/entrypoint.sh /etc/workstation-startup.d/200_claude-setup.sh`.

### Unchanged

`ws list`, `ws url`, `ws connect`, `ws logs`, `ws stop`, `ws delete` — none
of these touch build assets.

## Config discovery

`FindProjectConfig` walks up from the starting directory looking for
`.ws/config.yaml`.

| Situation | Behavior |
|---|---|
| `.ws/config.yaml` found | Load it. |
| Only `.ws.yaml` at the same level (no `.ws/config.yaml`) | Auto-migrate: `mkdir .ws/` (mode 0755) then `mv .ws.yaml .ws/config.yaml`. Print one-line stderr notice. Continue. |
| Both `.ws/config.yaml` and `.ws.yaml` at the same level | Error: `conflicting config: .ws/config.yaml and .ws.yaml both present — remove .ws.yaml`. |
| Neither found after walking up to stopDir | Existing error: `no .ws/config.yaml found; run 'ws setup' in your project root`. |

Migration is per-directory — if the walk-up finds `.ws.yaml` two levels up, it
migrates at that level, not at the starting directory.

## Embedding mechanics

```go
// internal/scaffold/embedded.go
package scaffold

import _ "embed"

//go:embed assets/Dockerfile
var embeddedDockerfile []byte

//go:embed assets/entrypoint.sh
var embeddedEntrypoint []byte
```

`internal/scaffold/assets/Dockerfile` and `internal/scaffold/assets/entrypoint.sh`
are the canonical files in the repo. No `go generate` step, no symlinks —
`go:embed` reads them directly at build time. `go build` and `make install`
work unchanged.

## Code layout

```
internal/scaffold/
├── assets/
│   ├── Dockerfile
│   └── entrypoint.sh
├── embedded.go          # go:embed directives
├── embedded_test.go     # sanity checks on embedded bytes
├── source.go            # ResolveSource(name) → (bytes, source)
├── source_test.go
├── scaffold_assets.go   # ScaffoldAsset(name, opts) — atomic write, modes, dir creation
├── scaffold_assets_test.go
├── scaffold.go          # existing interactive scaffolder (extended to prompt)
└── ... (existing files unchanged)

cmd/
├── setup.go             # add scaffold subcommand, extend interactive + template flows
├── setup_test.go        # extend
├── scaffold.go          # new: cobra wiring for `ws setup scaffold`
├── scaffold_test.go     # new
├── create.go            # add preflight check
└── create_test.go       # extend

internal/config/
├── discover.go          # walk up for .ws/config.yaml, handle migration
├── discover_test.go     # extend
├── migrate.go           # new: MigrateLegacyConfig helper
└── migrate_test.go      # new
```

## Error handling

### Scaffolding

| Situation | Behavior |
|---|---|
| Target file exists, no `--force` | Error: `.ws/<file> already exists; re-run with --force to overwrite`. Exit 1. Atomic — no other file in the same command is written after the first error. |
| `~/.config/ws/template/<file>` missing | Fall back to embedded default. No error. |
| `~/.config/ws/template/` partial (only one file present) | Use user template for that file, embedded default for the other. Log source per file. |
| Atomic rename fails (disk full, permission) | Propagate wrapped error. Temp file cleaned up. Original target file left untouched. |
| `.ws/` directory doesn't exist | Created at 0755 before writing. |
| User template unreadable (permission denied) | Propagate wrapped error (don't silently fall back — surface the intent mismatch). |

### Config migration

| Situation | Behavior |
|---|---|
| `mv .ws.yaml .ws/config.yaml` fails | Propagate wrapped error; original `.ws.yaml` untouched. |
| `.ws/` pre-exists but empty when migrating | Move still succeeds. |
| `.ws/config.yaml` pre-exists alongside `.ws.yaml` | Caught earlier by the both-present error. |

### ws create preflight

| Situation | Behavior |
|---|---|
| `.ws/Dockerfile` missing + TTY | Prompt. Yes → scaffold missing files (never overwriting existing ones) then continue. No → error. |
| `.ws/Dockerfile` missing + non-TTY | Error: `.ws/ not initialized — run 'ws setup scaffold' first`. |
| Only `.ws/entrypoint.sh` missing (Dockerfile present) | Same prompt/error, but scoped: scaffold only the missing file. |
| `docker buildx build` fails | Propagate exit code; no retry. |

### User template validation

None beyond `os.Stat`. Contents are treated as opaque bytes. If a user's
custom Dockerfile is malformed, `docker build` surfaces its own error.

## Testing

### `internal/scaffold` unit tests

Table-driven, using `t.TempDir()` — no real network, no real docker.

- **`ResolveSource`:** user-template present → returns user bytes; absent →
  returns embedded bytes; partial user-template dir → per-file precedence.
- **`ScaffoldAsset`:** target absent + write succeeds → file exists with
  expected mode (0644 Dockerfile, 0755 entrypoint.sh); target exists + no
  `--force` → error; target exists + `--force` → overwrites.
- **`--dry-run`:** no bytes written, summary returned.
- **Atomic write:** simulated rename failure leaves original untouched (temp
  file cleaned up).
- **`.ws/` creation:** missing → created at 0755; pre-existing at wrong mode
  → not re-chmoded.

### `internal/config` tests

Extend `discover_test.go` and add `migrate_test.go`:

- `FindProjectConfig` finds `.ws/config.yaml` at the starting directory.
- `FindProjectConfig` finds `.ws/config.yaml` several levels up.
- Only `.ws.yaml` present → auto-migrates, returns new path.
- Both `.ws/config.yaml` and `.ws.yaml` present → error.
- Migration failure case → original `.ws.yaml` untouched.

### `cmd/setup_test.go`

Extend existing tests:

- Interactive `ws setup`: `y` at the scaffold prompt writes both assets; `n`
  skips them.
- `ws setup template`: writes `.ws/` assets non-interactively.
- `ws setup template --global-only`: does NOT write `.ws/` assets.
- `--force` honored on both flows.

### `cmd/scaffold_test.go` (new)

- `ws setup scaffold` with no existing `.ws/` → both files written from
  embedded defaults.
- With `~/.config/ws/template/Dockerfile` present → Dockerfile from user
  template, entrypoint.sh from embedded default.
- Refuses when files exist without `--force`; succeeds with `--force`.
- `--dry-run` writes nothing.

### `cmd/create_test.go`

Extend existing tests:

- Preflight: missing `.ws/Dockerfile` + TTY stdin → scaffolder invoked, then
  build runs.
- Missing + non-TTY → errors without invoking docker.
- Build args include `-f .ws/Dockerfile` and `.` as context (via a fake
  docker runner).

### `internal/scaffold/embedded_test.go` (new)

- Both `embeddedDockerfile` and `embeddedEntrypoint` are non-empty.
- `embeddedDockerfile` contains `COPY .ws/entrypoint.sh` (guards against
  the COPY path drifting).
- `embeddedEntrypoint` starts with `#!/bin/bash` and contains
  `set -euo pipefail`.

### Out of scope for tests

- Real `docker build` invocations.
- Real `gcloud` calls.
- Golden-file diffs of the full Dockerfile / entrypoint.sh (too brittle —
  the embedded bytes are the source of truth, unit tests assert only invariants).

## Migration of this repo

The workspace repo currently hosts `Dockerfile` and `entrypoint.sh` at its
root. To dogfood the new design:

1. Move `Dockerfile` → `internal/scaffold/assets/Dockerfile` and edit the
   `COPY entrypoint.sh ...` line to `COPY .ws/entrypoint.sh ...`.
2. Move `entrypoint.sh` → `internal/scaffold/assets/entrypoint.sh` (no edits).
3. After building the new `ws` binary, run `ws setup scaffold` in this repo
   to produce `.ws/Dockerfile` and `.ws/entrypoint.sh`. Commit those.
4. The Makefile still runs `go build` → the new binary embeds the updated
   assets. `make install` is unchanged.

The repo ends up with two tracked copies of the files during the transition
— the canonical one at `internal/scaffold/assets/` and the project's own
dogfood copy at `.ws/`. That is by design: the assets directory is the
embedded default for all users, and `.ws/` is this project's own scaffolded
instance.

## Out of scope

- **Hooks or append-only overlays.** Full ownership only. If the community
  later wants hooks, they can be layered on top without breaking this design.
- **User-global defaults beyond file-copy.** `~/.config/ws/template/` is
  plain files, not templated. No variable substitution, no conditional logic.
- **Per-project image caching / reuse.** Each project builds and pushes its
  own image. Sharing base images across projects is out of scope.
- **Modifying existing `.ws/` files in place on `ws` upgrade.** User-owned
  files stay as the user left them. `ws setup scaffold --force` is the
  explicit re-scaffold path.
- **Secret scaffolding.** Claude token and GitHub PAT are still handled by
  `ws create` at first run, unchanged.
