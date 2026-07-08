# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`ra` is a Go CLI that provisions Google Cloud Workstations with a configurable, plugin-based runtime. It handles the full lifecycle: creating GCP infrastructure (Artifact Registry, Secret Manager, service accounts, workstation cluster/config/instance), building and pushing a Docker image, and seeding the container with credentials fetched at boot from Secret Manager.

`ra` itself ships with **no built-in plugins** — Claude Code, GitHub, and any other integrations are external plugins installed via `ra plugin install`.

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
- `~/.config/ra/config.yaml` — **required**; user-level baseline (loaded by `internal/config/global.go`). Missing → fatal error pointing at `ra setup`.
- `.ra/config.yaml` — optional project-level overrides (found by walking up from cwd in `internal/config/discover.go`). Absent project config is a non-error: `FindProjectConfig` returns `("", nil)` and `LoadProjectConfig("")` yields an empty struct so commands work from any directory.

`internal/config/merge.go` handles merging with field-level precedence. `internal/config/validate.go` enforces required fields and checks for env var collisions.

### On-disk layout

User-global and project-local both mirror the workstation image's `/etc/` layout:

```
{~/.config/ra,.ra}/
├── config.yaml
├── Dockerfile
├── Dockerfile.d/                     # build-time *.sh hooks (also from plugins)
├── workstation-startup.d/
│   └── 200_remote-agent-setup.sh     # generic ra entrypoint
├── profile.d/
│   └── 00-ra-wait.sh                 # login-shell sentinel wait
└── plugins/<name>/...                # installed via `ra plugin install`
```

### Build context assembly

`internal/scaffold/buildcontext.go` (`AssembleBuildContext`) produces an ephemeral docker build context each time `ra create` runs at `~/.cache/ra/build/<name>/` (never under the project dir, so creating from any cwd is safe). It merges core ra files with all installed plugins' contributions in priority order:

- **Single-file resolution** (`Dockerfile`): project (`.ra/Dockerfile`) → user (`~/.config/ra/Dockerfile`) → embedded.
- **Directory merge** (`Dockerfile.d/`, `workstation-startup.d/`, `profile.d/`): plugin files first, then user-global, then project-local — same-name files shadow.
- **Whole-plugin staging** (`plugins/<name>/`): project shadows user; the assembler skips `.ra-install.yaml` so install metadata never lands in the image.

`AssembleBuildContext` requires `BuildContextOptions.Dest` (output path); `Cwd` and `Home` are sourcing-only. The Dockerfile is generic and references build-context-relative paths. The cache dir is removed after every build (success or failure).

### Subcommands → Core Packages

- **`cmd/setup.go`** calls `scaffold.InteractiveScaffold()` — probes gcloud/git defaults, prompts for ra-only config (project_id, git identity), writes config YAMLs, and materializes core build assets (`Dockerfile`, `workstation-startup.d/200_remote-agent-setup.sh`, `profile.d/00-ra-wait.sh`) into `~/.config/ra/` and optionally `.ra/`. It does NOT prompt for plugin config — that moves to `ra plugin install`.
- **`cmd/plugin.go`** implements `ra plugin install/list/remove`. Install clones (or copies, for local paths), prompts for the install level (user/project), prompts for plugin config + secret values, uploads secret values to GCP Secret Manager, writes `plugins.<name>.*` and `secrets.*` blocks into the chosen-level `config.yaml`, and writes `<plugin-dir>/.ra-install.yaml` with source + install timestamp.
- **`cmd/create.go`** is the main provisioning pipeline: enable APIs → ensure Artifact Registry → build+push Docker image → ensure GCP secrets → create service account + IAM → ensure cluster+config → create workstation.
- **`cmd/teardown.go`** is the inverse of `create`: `ra teardown` deletes provisioned resources in reverse dependency order (workstation instances → config → cluster → project IAM bindings → service account → secrets → Artifact Registry repo). Crucially, it distinguishes **per-project** resources (this config + its workstation instances — always deleted) from **shared** resources (cluster, service account, project IAM, secrets, Artifact Registry repo — all named from user-level defaults and shared across projects). `create` provisions shared resources skip-if-exists (safe to share); teardown therefore deletes them **only when this is the last config on the cluster** (detected via `Client.ListConfigs`) or when `--delete-shared` forces it — otherwise a teardown from one project would break siblings. It is check-then-delete (skips absent resources, so re-runs converge) and best-effort (collects failures, still attempts every step). Enabled APIs are left on (project-wide, unsafe to disable); GCS buckets are preserved unless `--delete-buckets` (contents unrecoverable). Flags: `--force`, `--dry-run`, `--delete-buckets`, `--keep-secrets`, `--delete-shared`, plus the shared `--project/--cluster/--config` overrides. Deleting the **cluster** is what stops the fixed control-plane fee. The `baseProjectRoles` list and `serviceAccountEmailFor` / `confirmYes` helpers are shared with `cmd/create.go` / `cmd/delete.go` so the two sides can't drift. See [docs/adr/0010](docs/adr/0010-ra-teardown-shared-safe-resource-removal.md) for the design rationale.
- All gcloud operations go through `internal/gcloud/` — `runner.go` shells out to the `gcloud` binary; `workstation.go` wraps it with typed methods.
- `internal/secrets/upload.go` centralizes GCP Secret Manager interaction (used by both `ra create` and `ra plugin install`).
- `internal/configwrite/append.go` provides `MergePluginConfig` and `RemovePluginConfig` for safe YAML edits to `config.yaml`.

### Plugin layout

A plugin's directory mirrors the image destinations its scripts land in:

```
<plugin>/<name>/
├── plugin.yaml                       # required: schema, config, secrets
├── Dockerfile.d/<NNN>_<purpose>.sh   # optional: build-time hooks
├── workstation-startup.d/<NNN>_<purpose>.sh  # optional: runtime startup scripts
└── profile.d/<NNN>_<name>.sh         # optional: login-shell scripts
```

Plugin scripts in `workstation-startup.d/` run independently from `200_remote-agent-setup.sh` (they drop into `/etc/workstation-startup.d/` directly). Each plugin script that needs fetched secrets must source `/run/ra/env` itself:

```bash
[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a
[ "${RA_PLUGIN_<NAME>_ENABLED:-false}" = "true" ] || exit 0
```

Plugin scripts in `profile.d/` run inside the user's interactive login shell. They do NOT need to source `/run/ra/env` themselves — `00-ra-wait.sh` (shipped by ra core) waits for the entrypoint sentinel and sources it before any later profile.d script runs. `RA_PLUGIN_<NAME>_*` config vars are propagated from PID-1 env into `/run/ra/env` and `/etc/environment` by `200_remote-agent-setup.sh` (driven by the `RA_PROPAGATE_KEYS` allowlist that `cmd/create.go buildContainerEnv` emits), so login-shell gates on those flags Just Work.

Plugins are **not** shipped in this repository. They live in their own repo — [`adamtait/ra-plugins`](https://github.com/adamtait/ra-plugins) — a monorepo with one subdirectory per plugin (claude, github, aws, …). Install one with the repo + subpath form, e.g. `ra plugin install https://github.com/adamtait/ra-plugins github`. The github plugin (Dockerfile.d/100_github-cli.sh + workstation-startup.d/250_github-credential-helper.sh + workstation-startup.d/260_github-clone.sh) holds what used to be hardcoded in core: gh CLI install, the PAT git credential helper, and first-boot repo clone. A workstation only does GitHub things if the user installs that plugin.

### Zero-Secrets Design

Credentials never appear in config. Each secret has a logical name and env var name in config; the actual value lives in GCP Secret Manager. The workstation's service account has IAM access to fetch secrets at boot. `200_remote-agent-setup.sh` (embedded in binary, materialized into the build context) runs at every container start to fetch secrets and write them to `/etc/environment` + `/run/ra/env`.

### Naming

`internal/naming/naming.go` generates GCP-compliant workstation names ≤63 chars using memorable adjective+noun pairs (`GenerateMemorable`) or a base name with a unique suffix (`UniqueSuffixed`).

## Key Constraints

- All gcloud calls shell out — no GCP Go SDK. Keep it that way.
- Provisioning steps are idempotent (skip-if-exists) except workstation creation itself.
- `ra` ships with no embedded plugins. Plugin defaults are an external concern.
- Plugin config lives at `plugins.<name>.*` in YAML; auth method (when applicable) is set via `plugins.<name>.auth_provider`.
- Scalar plugin config values are serialized as `RA_PLUGIN_<NAME>_<FIELD>=<value>`; `string_list` values are pipe-joined into the same env var, so list values may not themselves contain `|` (validated by `validateConfig`).
- `/run/ra/env` is sourced by login shells via `set -a; . FILE`; `ra_env_set` single-quote-wraps values when writing it so whitespace and shell metacharacters survive sourcing intact.
- Env-var persistence lives in the shared `/usr/local/lib/ra/ra-env.sh` library, which `200_remote-agent-setup.sh` emits at boot and sources, and which plugin scripts source too. Use `ra_env_set KEY VALUE` / `ra_env_unset KEY` (each dedups and updates BOTH `/etc/environment` and `/run/ra/env`) instead of writing those files by hand.
- Plugin scripts in `workstation-startup.d/` run independently after `200_`; they must source `/run/ra/env` and gate on `RA_PLUGIN_<NAME>_ENABLED` themselves.
- A plugin can advertise a single in-workstation TCP port via `port-for-tunnel: <N>` at the top level of `plugin.yaml`. `ra plugin install` renders `plugins.<name>.port-for-tunnel: <N>` into `config.yaml` (so the user can override). The value is exported as `RA_PLUGIN_<NAME>_PORT_FOR_TUNNEL` for the plugin's startup scripts, and `ra tunnel <plugin-name>` reads the configured value (with schema fallback) via `Registry.PortByName`. See [docs/adr/0001-ra-tunnel-and-plugin-port-field.md](docs/adr/0001-ra-tunnel-and-plugin-port-field.md).
- **`ra connect`** runs `connect.login_command` (client-side config, in `internal/config`'s `ConnectConfig`) on the workstation in place of the login shell (`Client.SSH` appends `--command <cmd> --ssh-flag=-t`); unset opens a plain interactive login shell. It's read in `cmd/connect.go` and never propagated to the workstation. There is no `--et` in core.
- **Plugin-defined commands** — a plugin declares `commands:` in `plugin.yaml`; `ra` registers each as `ra plugin <plugin-name> <command-name>` (in `cmd/plugin_commands.go`, at `init()` before `Execute()`). On invoke it optionally resolves + ensures the workstation running (`needs_workstation:`), then execs the plugin's local `run:` script with an env contract (`RA_WORKSTATION/PROJECT/REGION/CLUSTER/CONFIG/WORKSTATION_USER/LOGIN_COMMAND`, and `ra` on `PATH`). Core stays plugin-agnostic (no plugin name hard-coded); a plugin shadowing `install`/`list`/`remove` is skipped with a warning. A plugin may also declare `post_install_message:` (printed by `ra plugin install`). The Eternal Terminal transport lives in the `et` plugin as `ra plugin et connect`. See [docs/adr/0014-connection-config-and-plugin-commands.md](docs/adr/0014-connection-config-and-plugin-commands.md).
