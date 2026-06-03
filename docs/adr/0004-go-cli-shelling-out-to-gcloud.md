# ADR 0004: A Go CLI shelling out to `gcloud`, replacing the provisioning bash script

- **Status:** Accepted (retrospective record; provisioning model still current, config schema superseded by [ADR 0006](0006-layered-config-and-secret-wire-protocol.md))
- **Date:** 2026-04-21
- **Deciders:** @adamtait

> **Retrospective note.** This ADR backfills the founding decision from the
> project's original design spec (the now-removed
> `docs/superpowers/specs/2026-04-21-cws-cli-design.md`, recoverable from git
> history at `334634a^`). The tool was named `cws`, then `ws`, and finally
> `ra`; read those names as `ra` throughout. The two-file config *layering*
> here was reshaped by [ADR 0006](0006-layered-config-and-secret-wire-protocol.md);
> the shell-out-to-gcloud contract and walk-up project discovery remain in force
> (see CLAUDE.md).

## Context

The project began as `create-workstation.sh`, a single bash script that
provisioned a Google Cloud Workstation pre-loaded with a dev runtime. The
script worked but had no structure: no subcommands, no per-project
configuration, and it had to be run from one specific repo. We wanted a tool
that any developer could install once and run from any project (or
subdirectory) to provision and manage workstations.

Two cross-cutting constraints framed every later decision:

- **Auth and GCP surface area.** Re-implementing GCP calls against a Go SDK
  would mean owning auth, retries, and API drift. The user already has an
  authenticated `gcloud` session; delegating to it is free and keeps behavior
  identical to running the commands by hand.
- **Per-project vs per-user settings.** Project identity (GCP project, repo
  URL) belongs in the repo; user identity (git name/email, machine defaults)
  does not. A single config file cannot serve both.

## Decision

Build `ra` as a Go CLI (cobra) that **shells out to the `gcloud` binary for
every GCP operation** — no GCP Go SDK, ever. All `gcloud` calls go through a
`Runner` interface (`internal/gcloud/runner.go`) so commands are unit-testable
with a fake that records invocations; `internal/gcloud/workstation.go` wraps it
with typed methods. `gcloud` stdout/stderr streams straight to the terminal,
and a non-zero exit propagates the failed command and its exit code.

### Subcommand surface

`create`, `list`, `connect`, `url`, `logs`, `stop`, `delete` — provisioning
plus lifecycle management. `create` is idempotent (every step checks for
existence first) except the final workstation creation.

### Two-file layered config

- **Project config** (committed to the repo) carries project-only fields
  (`project_id`, repo URL). Discovered by **walking up the directory tree** from
  `$PWD` until found or the filesystem root is reached — mirroring how `git`
  finds `.git`, so the tool works from any subdirectory.
- **User config** (`~/.config/<tool>/config.yaml`, per-machine, gitignored)
  carries user identity and machine defaults.

The two merge with a defined precedence (CLI flags → project → user →
hardcoded defaults), so a project inherits sensible defaults without
re-declaring them.

### Name resolution

A workstation's default name is derived from project + user, sanitized to
GCP's `[a-z0-9-]` and truncated to the 63-char limit. Every workstation-
targeting command accepts an optional positional name; when omitted and exactly
one workstation exists, it is selected automatically, otherwise the user is
prompted.

## Alternatives considered

- **Keep the bash script.** Rejected: no path to per-project config, subcommand
  structure, or testable provisioning logic.
- **GCP Go SDK instead of shelling out.** Rejected: owns auth and API drift for
  no benefit over the user's existing `gcloud` session. This became a permanent
  project rule (CLAUDE.md: "All gcloud calls shell out — keep it that way").
- **Single config file.** Rejected: cannot separate committed per-project
  settings from per-machine user identity.

## Consequences

- `gcloud` and `docker` are hard runtime dependencies — the same ones the
  original script required, so no regression.
- The `Runner` seam makes the entire provisioning pipeline unit-testable
  without touching GCP; this shaped the test strategy for every later feature.
- Walk-up discovery plus name resolution let the tool run from anywhere, which
  every subsequent command relied on.
- The specific config *schema* defined here (flat `defaults:`/`projects:` maps,
  hardcoded Go defaults) was replaced wholesale by the grouped, exhaustive,
  no-hardcoded-defaults model in [ADR 0006](0006-layered-config-and-secret-wire-protocol.md).
  The *layering principle* (project overrides user) survived the rewrite.
