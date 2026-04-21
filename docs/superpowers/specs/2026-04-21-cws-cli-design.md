# cws CLI Design

**Date:** 2026-04-21
**Status:** Approved

## Overview

`cws` is a Go CLI binary that provisions and manages Google Cloud Workstations pre-loaded with Claude Code. It replaces the existing `create-workstation.sh` bash script with a structured, multi-subcommand tool that supports per-project configuration and can be invoked from any directory (or subdirectory) within a project.

All GCP operations shell out to the `gcloud` CLI — no GCP Go SDK. Auth is fully delegated to the user's existing `gcloud` session.

## Project Layout

```
cws/
├── main.go
├── go.mod
├── cmd/
│   ├── root.go               # cobra root, config loading
│   ├── create.go             # cws create
│   ├── connect.go            # cws connect [name]
│   ├── url.go                # cws url [name]
│   ├── logs.go               # cws logs [name]
│   ├── stop.go               # cws stop [name]
│   ├── delete.go             # cws delete [name]
│   └── list.go               # cws list
├── internal/
│   ├── config/
│   │   ├── discover.go       # walk up dirs to find .workstation.yaml
│   │   ├── merge.go          # merge project + global config
│   │   └── types.go          # Config struct
│   └── gcloud/
│       ├── runner.go         # exec wrapper: run gcloud, stream output
│       └── workstation.go    # typed wrappers: List, Describe, Start, Stop, etc.
└── Makefile                  # build, install to ~/.local/bin
```

## Config System

### Project config — `.workstation.yaml` (checked into git, no secrets)

```yaml
project_id: my-gcp-project
region: us-central1
cluster_name: claude-dev-cluster
config_name: claude-dev-config
github_repo_url: https://github.com/org/repo.git
machine_type: e2-standard-4    # optional, default: e2-standard-4
disk_size_gb: 50               # optional, default: 50
```

Placed at the root of a project repository. The tool walks up the directory tree from `$PWD` to find it, stopping at `$HOME` or filesystem root. This mirrors how `git` discovers `.git`.

### Global user config — `~/.config/cws/config.yaml` (gitignored, per-machine)

```yaml
defaults:
  region: us-central1
  git_user_name: Adam Tait
  git_user_email: adam@example.com

projects:
  my-gcp-project:
    service_account_name: claude-workstation-sa
    image_name: claude-code-workstation
    image_tag: latest
    repo_name: claude-workstation-images
```

`git_user_name` and `git_user_email` live here (not in the project config) because they are per-user identity, not per-project settings.

### Merge precedence (highest to lowest)

1. CLI flags / env vars
2. Global config `projects.<project_id>` overrides
3. Project config (`.workstation.yaml`)
4. Global config `defaults`
5. Hardcoded defaults (e.g., `machine_type: e2-standard-4`)

### Workstation name

Default name: `<project_id>-<username>`, where `<username>` is `$(whoami)` sanitized to `[a-z0-9-]`. The combined name is truncated to GCP's 63-character limit. Any command that targets a workstation accepts an optional positional argument to override the default name.

## Commands

### `cws create`

Provisions a complete workstation environment. Idempotent — safe to re-run; each step checks for existence first. Requires `.workstation.yaml` to exist in the project root (the user creates this file before first run). Required fields (`project_id`, `github_repo_url`) can also be supplied via env vars as a fallback, matching the current script's behavior. Ports all steps from `create-workstation.sh`:

1. Enable required GCP APIs
2. Ensure Artifact Registry repo exists
3. Build and push Docker image (`linux/amd64`)
4. Ensure secrets exist in Secret Manager (prompts interactively if missing)
5. Ensure service account and IAM bindings
6. Ensure workstations cluster (warns that first creation takes ~20 min)
7. Ensure workstations config (create or update)
8. Create and start workstation
9. Print access info (browser URL, SSH command, stop command)

Claude auth mode (OAuth vs API key) is detected from existing secrets and prompted if neither exists, matching current script behavior.

### `cws list`

Lists all workstations in the cluster with their current status (RUNNING, STOPPED, etc.). Reads cluster/region/project from merged config.

### `cws connect [name]`

Opens an SSH session to the workstation via `gcloud workstations ssh`. Defaults to `<project_id>-<username>`. If name is omitted and the default doesn't exist but exactly one workstation is in the cluster, connects to that one. If multiple exist, prints a numbered list to stderr and prompts for selection (e.g., `Select workstation [1-3]: `).

### `cws url [name]`

Prints the browser IDE URL (`https://80-<hostname>`). Same name resolution as `connect`. Does not start the workstation if stopped.

### `cws logs [name] [--tail=N]`

Streams container logs from Cloud Logging via `gcloud logging read` with `--follow`. `--tail=N` prints the last N lines and exits instead of streaming. Same name resolution as `connect`.

### `cws stop [name]`

Stops a running workstation. Same name resolution as `connect`.

### `cws delete [name]`

Deletes a workstation. Prompts `Are you sure? [y/N]` on interactive terminals. Requires `--force` for non-tty use. Same name resolution as `connect`.

## Error Handling

- `gcloud` stdout/stderr streams directly to the terminal — same UX as running the script by hand.
- On non-zero `gcloud` exit, `cws` prints the failed command and exits with the same code.
- Missing required config fields produce a clear validation error before any GCP call is made.
- No `.workstation.yaml` found in the directory tree produces a clear error: `no .workstation.yaml found; create one in your project root (see README)`.
- `delete` without `--force` on a non-tty exits with an error rather than silently proceeding.

## Testing

- Unit tests for config discovery (walk-up algorithm), merge precedence, and workstation name sanitization/generation. No GCP calls.
- The `gcloud` runner is behind a `Runner` interface so commands can be unit tested with a fake that records invocations and returns canned output.
- Integration tests against real GCP are out of scope for initial implementation.

## Distribution

Built with `make build` → `cws` binary. Installed to `~/.local/bin` via `make install`. No external runtime dependencies beyond `gcloud` and `docker` (both already required by the existing script).
