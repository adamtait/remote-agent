# ra — Remote Agent

`ra` is a simple CLI tool for moving CLI coding agents to the cloud. It's built on the rock-solid Google Cloud Workstations to keep your agents safely contained in a work-friendly environment.

`ra` is a Go CLI that provisions a browser-accessible Google Cloud Workstation with the development tooling of your choice, plus a GitHub repo cloned and ready to go. Tooling is plugin-based: `ra` itself ships with no built-in plugins; you add Claude Code, GitHub-specific helpers, or any other integration via `ra plugin install`.



## Design

**Zero secrets in the workstation config.** All credentials live only in GCP Secret Manager. The container resolves them at boot via its attached service account.

**Plugin contributions mirror the image.** A plugin's directory contains
`Dockerfile.d/`, `workstation-startup.d/`, and `profile.d/` subtrees whose
contents land at the matching paths in the final image (`/etc/...`).

**Generic core, fat plugins.** The core ra Dockerfile and entrypoint are
plugin-agnostic. Anything tool-specific (Node.js install, custom PATH entries,
auth flows) lives in the relevant plugin.

**Git Repo Aware.** `ra` makes it easy to create and connect to remote workstations by project.


## Prerequisites

- `gcloud` CLI authenticated (`gcloud auth login`)
- GCP project with billing enabled
- `docker` with buildx (for cross-arch builds on macOS)


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


## Files

- `main.go` / `cmd/` / `internal/` — `ra` CLI (idempotent provisioning)
- `Makefile` — `make install` builds and drops the binary into `~/.local/bin`
- `internal/scaffold/assets/` — embedded core build assets (Dockerfile, `workstation-startup.d/200_remote-agent-setup.sh`, `profile.d/00-ra-wait.sh`)
- `plugins/claude/`, `plugins/github/` — in-tree homes of the Claude Code and GitHub plugins (each will move to its own repo in a follow-up changeset)
- `~/.config/ra/` — your user-global ra config + materialized core assets + installed plugins
- `.ra/` — project-local ra config + (optional) overrides + installed plugins


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

## Creating Plugins

A plugin is a directory containing a `plugin.yaml` and optional shell scripts.
Author one locally and install it with `ra plugin install ./path/to/plugin`.

### Directory structure

```
my-plugin/
├── plugin.yaml                          # required
├── Dockerfile.d/
│   └── 100_my-plugin-install.sh         # optional: build-time hooks
├── workstation-startup.d/
│   └── 250_my-plugin-setup.sh           # optional: per-boot runtime setup
└── profile.d/
    └── 00-my-plugin-env.sh              # optional: login-shell additions
```

Plugin scripts mirror where they land in the image (`/etc/...`):

| Directory | When it runs | Use it for |
|---|---|---|
| `Dockerfile.d/` | Docker image build time | Installing packages, downloading binaries |
| `workstation-startup.d/` | Every container boot, after `200_remote-agent-setup.sh` | Auth setup, credential helpers, first-boot data clone |
| `profile.d/` | Every login shell | PATH additions, shell aliases |

### plugin.yaml schema

```yaml
name: my-plugin           # required; becomes RA_PLUGIN_<NAME>_* prefix
version: "1.0.0"
description: "What this plugin does"

config:
  - name: enabled         # include in every plugin
    type: bool            # bool | string | int
    description: "Enable my plugin?"
    default: false
  - name: my_setting
    type: string
    description: "A string option"
    default: ""
    options: [alpha, beta, gamma]  # omit for free-form input

secrets:
  - name: my_token
    env_var: MY_TOKEN
    description: "API token for my service"
    default_secret_name: my-plugin-token   # GCP Secret Manager name
    value_command:                         # optional: local install-time capture
      command: my-cli
      args: ["login"]
      output: stdout                       # stdout | file_base64
      path: "~/.my-cli/auth.json"           # required for file_base64
      validate: json                       # optional: json
      interactive: true

auth_providers:            # omit if no auth choice is needed
  - name: apikey
    description: "Static API key"
    secrets:
      - name: my_apikey
        env_var: MY_APIKEY
        description: "API key from my service's console"
        default_secret_name: my-plugin-apikey
```

`ra plugin install` prompts for each `config` field, the chosen `auth_provider` (if any), and secret values (hidden input). If a field or secret declares `value_command`, `ra` runs that local command during install and uses its output instead of prompting. Config is written to `config.yaml`; secret values go to GCP Secret Manager.

### Script naming conventions

Script filenames follow `<NNN>_<plugin-name>-<purpose>.sh`. Numbers control execution order within the directory.

- **`Dockerfile.d/`** — use `100`–`199`. Scripts from all installed plugins merge into a single directory and run in sorted order during `docker build`.
- **`workstation-startup.d/`** — use `250`+. The core `200_remote-agent-setup.sh` runs first; plugin scripts must be numbered ≥ 250 to guarantee secrets are available.
- **`profile.d/`** — use `10`+ for plugin additions (`00`–`09` is reserved for the core `00-ra-wait.sh`).

### Runtime environment contract

`ra create` injects plugin configuration as container env vars:

```
RA_PLUGIN_<UPPERCASE_NAME>_<UPPERCASE_FIELD>=<value>   # config fields
RA_PLUGIN_<UPPERCASE_NAME>_AUTH_PROVIDER=<provider>    # if auth_providers used
RA_SECRET_<UPPERCASE_LOGICAL>_NAME=<gcp-secret-name>   # secret metadata
RA_SECRET_<UPPERCASE_LOGICAL>_VAR=<env-var-name>
```

Secret *values* are fetched from GCP Secret Manager at each boot by `200_remote-agent-setup.sh` and written to `/run/ra/env`. Your startup script reads them by sourcing that file.

**`Dockerfile.d/` scripts do not have access to `RA_PLUGIN_*` env vars.** Build-time behavior (e.g. which binary version to install) must be hardcoded in the script.

### Standard startup script preamble

Every `workstation-startup.d/` script should open with:

```bash
[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a
[ "${RA_PLUGIN_MY_PLUGIN_ENABLED:-false}" = "true" ] || exit 0
```

Line 1 sources fetched secrets (safe before `200_` runs — the file simply won't exist yet). Line 2 exits immediately if the plugin is disabled.

### First-boot markers

For work that should run only once per workstation lifetime (cloning a repo, expensive initial setup):

```bash
FIRST_BOOT_MARKER="/home/user/.ra-my-plugin-first-boot-done"
[ -e "${FIRST_BOOT_MARKER}" ] && exit 0

# ... first-boot work ...

touch "${FIRST_BOOT_MARKER}"
chown user:user "${FIRST_BOOT_MARKER}"
```

### Tips

- **Always gate on `RA_PLUGIN_<NAME>_ENABLED`.** Without it, your script runs on every workstation even when the plugin is not installed by that user.
- **Every-boot scripts must be idempotent.** They run on every container start. Overwrite config files (`>`), don't append (`>>`).
- **`Dockerfile.d/` scripts run as root** with no user home, no secrets, and no runtime env. Keep them to system-level installs only.

### Installing and testing locally

```bash
ra plugin install ./plugins/my-plugin --user   # install from local path
ra plugin list                                  # confirm installed
ra create                                       # rebuild image + reprovision

ra plugin remove my-plugin                      # remove when done
```

To test a startup script on a running workstation:

```bash
ra connect
sudo bash /etc/workstation-startup.d/250_my-plugin-setup.sh
```

## Rotating secrets

All rotations happen via Secret Manager — no need to re-run `ra create`.

For plugins with command-generated credentials, re-run the plugin install command to capture and upload a new secret version:

```bash
ra plugin install codex --force
```

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
