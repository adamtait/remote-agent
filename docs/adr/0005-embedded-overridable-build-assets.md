# ADR 0005: Embedded, fully-overridable build assets via scaffolding

- **Status:** Accepted (retrospective record; **superseded** by [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md))
- **Date:** 2026-04-22
- **Deciders:** @adamtait

> **Retrospective note.** Backfilled from the now-removed
> `docs/superpowers/specs/2026-04-22-ws-project-scaffolding-design.md`
> (git `334634a^`). The full-override-via-`go:embed` decision below is the
> direct ancestor of today's build-context assembler, but the concrete shape it
> introduced — a per-project `.ws/Dockerfile` + `.ws/entrypoint.sh`, a
> `~/.config/.../template/` user-baseline dir, and a project-root docker build
> context — was replaced by the image-mirror layout in
> [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md). Tool named
> `ws` here; read as `ra`.

## Context

After [ADR 0004](0004-go-cli-shelling-out-to-gcloud.md), `ra create` still ran
`docker build .` against the current directory and expected a `Dockerfile` and
`entrypoint.sh` to live at the repo root. That tied the tool to the one repo
that happened to contain those files and blocked two goals:

1. Run `ra create` in **any** git root and get a workstation for that repo.
2. Let humans **and AI agents** fully control what the image contains
   (Dockerfile) and what runs at container boot (entrypoint).

We needed default build assets that ship with the binary yet remain completely
editable per project.

## Decision

**Embed the canonical `Dockerfile` and `entrypoint.sh` in the binary via
`go:embed`** (from `internal/scaffold/assets/`), and add a scaffolding command
that materializes a **full, editable copy** into the project's `.ws/`
directory. The customization model is *full ownership*: no hooks, no overlays,
no append-only layers. Users and agents edit the scaffolded files freely;
upgrading the binary never touches them; re-running scaffold with `--force` is
the explicit path to refresh from current defaults.

### Per-file resolution precedence

Each asset resolves independently:

1. `~/.config/<tool>/template/<file>` — a personal user baseline, if present.
2. Embedded default.

A user who wants a custom Dockerfile but the stock entrypoint provides only the
former; the other falls back to embedded.

### Scaffolding surface

- A dedicated `setup scaffold` command (writes the assets; `--force`,
  `--dry-run`).
- The interactive `setup` flow prompts to scaffold after writing config.
- `create` preflights for the assets: on a TTY it offers to scaffold the
  missing ones; non-interactively it errors with a clear "not initialized"
  message rather than failing opaquely inside `docker build`.

Assets are written atomically (temp file in the target dir, then rename) with
correct modes (`0644` Dockerfile, `0755` entrypoint), so a partial write can
never corrupt an existing file under `--force`.

### Embedding mechanics

`internal/scaffold/assets/{Dockerfile,entrypoint.sh}` are the single source of
truth, read directly by `//go:embed` at build time — no `go generate`, no
symlinks. The project dogfoods its own design: it scaffolds its own `.ws/` from
the binary like any other repo.

## Alternatives considered

- **Hooks / append-only overlays** (ship a base, let users inject snippets).
  Rejected for v1 as more machinery than the problem needed; full ownership is
  simpler and can have hooks layered on later without breaking it.
- **Templated user-global assets** (variable substitution in
  `~/.config/.../template/`). Rejected: plain file-copy is predictable; no
  templating engine to reason about.
- **No embedding — require the user to author a Dockerfile.** Rejected: defeats
  "works in any repo out of the box."

## Consequences

- `ra` works in any git root: scaffold (or auto-scaffold) produces a complete,
  editable `.ws/` and `create` builds from it.
- Two tracked copies of the assets coexist in this repo by design: the
  canonical `internal/scaffold/assets/` (the embedded default for everyone) and
  the project's own dogfood `.ws/`.
- **Superseded by [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md):**
  the `.ws/{Dockerfile,entrypoint.sh}` pair and the `template/` user-baseline
  dir were replaced by a directory-merge layout (`Dockerfile.d/`,
  `workstation-startup.d/`, `profile.d/`) assembled into an ephemeral build
  context. What *carried forward* is this ADR's core bet: ship editable
  defaults, let the project fully override them, never mutate user-owned files
  on upgrade.
