# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`ra` is a Go CLI that provisions Google Cloud Workstations with Claude Code preinstalled. It handles the full lifecycle: creating GCP infrastructure (Artifact Registry, Secret Manager, service accounts, workstation cluster/config/instance), building and pushing a Docker image, and seeding the container with credentials fetched at boot from Secret Manager.

## Commands

```bash
make build    # compile → ./ra
make install  # install to ~/.local/bin/ra
make test     # go test ./...
make clean    # remove ./ra binary
```

Run a single test:
```bash
go test ./internal/config/... -run TestMerge
```

## Architecture

### Config Layering

Two config files are merged at startup (project overrides user):
- `~/.config/ra/config.yaml` — user-level baseline (loaded by `internal/config/global.go`)
- `.ra/config.yaml` — project-level overrides (found by walking up from cwd in `internal/config/discover.go`)

`internal/config/merge.go` handles merging with field-level precedence. `internal/config/validate.go` enforces required fields and checks for env var collisions.

### Subcommands → Core Packages

- **`cmd/setup.go`** calls `scaffold.InteractiveScaffold()` — probes gcloud/git defaults, prompts for missing fields, writes config YAMLs and materializes embedded assets (Dockerfile, entrypoint.sh, profile.d scripts) into `.ra/`
- **`cmd/create.go`** is the main provisioning pipeline: enable APIs → ensure Artifact Registry → build+push Docker image → create GCP secrets → create service account + IAM → ensure cluster+config → create workstation
- All gcloud operations go through `internal/gcloud/` — `runner.go` shells out to the `gcloud` binary; `workstation.go` wraps it with typed methods

### Zero-Secrets Design

Credentials never appear in config. Each secret has a logical name and env var name in config; the actual value lives in GCP Secret Manager. The workstation's service account has IAM access to fetch secrets at boot. `entrypoint.sh` (embedded in binary, materialized to `.ra/entrypoint.sh`) runs at every container start to fetch secrets and seed auth state.

### Embedded Assets

`internal/scaffold/embedded.go` embeds `internal/scaffold/assets/` (Dockerfile, entrypoint.sh, profile.d/) into the binary. `scaffold.ScaffoldAssets()` materializes them; users can then edit freely. `ResolveSource()` in `source.go` prefers user-edited files over embedded defaults.

### Naming

`internal/naming/naming.go` generates GCP-compliant workstation names ≤63 chars using memorable adjective+noun pairs (`GenerateMemorable`) or a base name with a unique suffix (`UniqueSuffixed`).

## Key Constraints

- All gcloud calls shell out — no GCP Go SDK. Keep it that way.
- Provisioning steps are idempotent (skip-if-exists) except workstation creation itself.
- Secrets config supports exactly one of `claude_oauth` or `claude_apikey` (not both).
- The entrypoint distinguishes OAuth vs API key mode by which env var is present at runtime.
