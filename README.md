# ra — Remote Agent on Google Cloud Workstations

`ra` is a Go CLI that provisions a browser-accessible Google Cloud Workstation with the development tooling of your choice, plus a private GitHub repo cloned and ready to go. Tooling is plugin-based: `ra` itself ships with no built-in plugins; you add Claude Code, GitHub-specific helpers, or any other integration via `ra plugin install`.

## Design

**Zero secrets in the workstation config.** All credentials live only in GCP Secret Manager. The container resolves them at boot via its attached service account.

**Plugin contributions mirror the image.** A plugin's directory contains
`Dockerfile.d/`, `workstation-startup.d/`, and `profile.d/` subtrees whose
contents land at the matching paths in the final image (`/etc/...`).

**Generic core, fat plugins.** The core ra Dockerfile and entrypoint are
plugin-agnostic. Anything tool-specific (Node.js install, custom PATH entries,
auth flows) lives in the relevant plugin.

## Files

- `main.go` / `cmd/` / `internal/` — `ra` CLI (idempotent provisioning)
- `Makefile` — `make install` builds and drops the binary into `~/.local/bin`
- `internal/scaffold/assets/` — embedded core build assets (Dockerfile, `workstation-startup.d/200_remote-agent-setup.sh`, `profile.d/00-ra-wait.sh`)
- `plugins/claude/`, `plugins/github/` — in-tree homes of the Claude Code and GitHub plugins (each will move to its own repo in a follow-up changeset)
- `~/.config/ra/` — your user-global ra config + materialized core assets + installed plugins
- `.ra/` — project-local ra config + (optional) overrides + installed plugins

## Prerequisites

- `gcloud` CLI authenticated (`gcloud auth login`)
- `docker` with buildx (for cross-arch builds on macOS)
- A GCP project with billing enabled
- A GitHub fine-grained PAT scoped to the target repo with Contents: Read & write
- If your Google Workspace/Cloud org enforces a session-length / reauth
  policy, the `reauth.googleapis.com` API must be enabled in the workstation
  project. `ra create` enables it automatically; otherwise gcloud's OAuth
  token refresh (e.g. during `gcloud workstations ssh`) will fail with
  `PERMISSION_DENIED: Reauth API has not been used...`.

## Getting started

```bash
make install                                 # installs ra to ~/.local/bin
ra setup                                     # writes config.yaml + core assets
ra plugin install ./plugins/github --user    # installs gh CLI + PAT credential helper,
                                             #   prompts for repo_url and PAT
ra plugin install ./plugins/claude --user    # install the in-tree claude plugin
ra create                                    # provisions the workstation
```

`ra setup` prompts for the minimum ra needs:
1. **GCP project ID** — the project where the workstation cluster lives
2. **Git identity** — name and email for commits inside the workstation

It writes:
- **`~/.config/ra/config.yaml`** — your personal baseline
- **`.ra/config.yaml`** — project-specific settings (committed to source control)
- The core build assets (`Dockerfile`, `workstation-startup.d/200_remote-agent-setup.sh`, `profile.d/00-ra-wait.sh`) into `~/.config/ra/`

`ra plugin install <source>` (where `<source>` is a git URL or a local
filesystem path):
1. Acquires the plugin (clone or copy)
2. Asks whether to install at user level (`~/.config/ra/plugins/`) or project level (`.ra/plugins/`)
3. Prompts for the plugin's config (e.g. `enabled: true`, `auth_provider: oauth`)
4. Prompts for secret values (hidden input)
5. Uploads secret values to GCP Secret Manager
6. Writes `plugins.<name>.*` and `secrets.*` blocks into the chosen-level `config.yaml`
7. Records install metadata at `<plugin-dir>/.ra-install.yaml`

`ra create` then assembles `.ra/.build/` (an ephemeral docker build context)
from your core assets + every installed plugin's contributions, builds and
pushes the image, ensures secrets and IAM bindings, and creates a workstation.
First run takes ~20 minutes (cluster provisioning is the slow part).
Subsequent runs skip everything that already exists.

## `ra plugin` reference

```bash
ra plugin install <git-url-or-path>          # interactive: choose user/project
ra plugin install <source> --user            # force user-level
ra plugin install <source> --local           # force project-level
ra plugin install <source> --force           # overwrite existing at chosen level

ra plugin list                               # show installed plugins, level, source
ra plugin remove <name>                      # remove a plugin + its config + secret entries
                                             #   (GCP Secret Manager values are NOT auto-deleted;
                                             #    `ra plugin remove` prints the gcloud commands)
```

## Day-to-day

```bash
ra list           # list workstations
ra url            # print the browser IDE URL
ra connect        # SSH into the workstation
ra logs           # tail workstation logs
ra stop           # stop (save $)
ra delete         # delete the workstation
```

Each targeted command accepts an optional name argument; otherwise the default workstation is used.

## Rotating secrets

All rotations happen via Secret Manager — no need to re-run `ra create`.

```bash
echo -n 'newvalue' | gcloud secrets versions add <secret-name> \
    --data-file=- --project=$PROJECT_ID
```

Rotation behavior depends on how each secret is consumed:
- **`github_pat`** is fetched per-git-operation via a credential helper; rotation is picked up on the next push/pull/clone, no restart.
- **API key plugins** (e.g. claude apikey mode) typically use a helper that re-reads the secret periodically; rotation is transparent within minutes.
- **Other secrets** are fetched at boot and exported to `/etc/environment` + `/run/ra/env`. Rotation requires `ra stop` + `ra create` (or `gcloud workstations start`) to take effect.

## How auth works at runtime

At every workstation boot:

1. The base image's startup machinery runs `/etc/workstation-startup.d/200_remote-agent-setup.sh`.
2. That script iterates `RA_SECRETS` (passed via container env from `ra create`), fetches each from Secret Manager, and writes them to `/etc/environment` + `/run/ra/env`. Plugin scripts in `/etc/workstation-startup.d/` (numbered ≥ 250) run after 200_; they fetch their own helpers and gate on `RA_PLUGIN_<NAME>_ENABLED`. The github plugin, for example, installs a per-git-op credential helper that fetches the PAT fresh from Secret Manager.
3. Other `/etc/workstation-startup.d/` scripts (numbered ≥ 250, contributed by plugins) run independently. Each plugin script sources `/run/ra/env` and gates on `RA_PLUGIN_<NAME>_ENABLED`.
4. The script writes `/run/ra/ready`, unblocking login shells via `/etc/profile.d/00-ra-wait.sh`.

## Cost notes

- Cluster: no standby cost when no workstations are running
- Workstation: billed per minute while running (e2-standard-4 ≈ $0.13/hr)
- Auto-stops after 2h idle by default
- Persistent home disk: billed even when stopped (~$0.04/GB-month for pd-standard)

## Security notes

The workstation config carries zero secret values — only secret names and non-sensitive metadata. Anyone with `workstations.configs.get` can read the config and learn *which* secrets are in use, but cannot read the secret values themselves; that requires `secretmanager.secretAccessor`, granted only to the workstation's service account.

To audit credential access, look at Secret Manager's data access audit logs — every fetch (boot-time, per-operation, periodic) appears there.
