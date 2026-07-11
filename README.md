# ra — Remote Agent

`ra` is a simple CLI tool for moving CLI coding agents to the cloud. It's built on the rock-solid Google Cloud Workstations to keep your agents safely contained in a work-friendly environment.

`ra` is a Go CLI that provisions a browser-accessible Google Cloud Workstation with the development tooling of your choice, plus a GitHub repo cloned and ready to go. Tooling is plugin-based: `ra` itself ships with no built-in plugins; you add Claude Code, GitHub-specific helpers, or any other integration via `ra plugin install`. Plugins live in their own repositories — see [`ra-plugins`](https://github.com/adamtait/ra-plugins) for the maintained set (Claude Code, GitHub, AWS, and more).



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

# Plugins live in the ra-plugins repo; install a plugin by repo + subdirectory:
ra plugin install https://github.com/adamtait/ra-plugins github --user
                                             #   installs gh CLI + PAT credential helper,
                                             #   prompts for repo_urls and PAT
ra plugin install https://github.com/adamtait/ra-plugins claude --user
                                             #   installs the claude plugin
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
ra list             # list workstations
ra validate         # Validate ra config files (user, project, and merged)
ra create           # Provision a Google Cloud Workstation
ra url              # print the browser IDE URL
ra connect          # SSH into the workstation
ra tunnel           # Open a local TCP tunnel to a workstation port
ra logs             # tail workstation logs
ra stop             # stop (save $)
ra delete           # delete the workstation
ra teardown         # delete all gcloud resources
```

Each targeted command accepts an optional name argument; otherwise the default workstation is used.

**`ra connect`** runs `connect.login_command` from your config in place of the login shell when set (e.g. to attach tmux); unset, it opens a plain interactive login shell. `ra setup` scaffolds it with a commented example.

**Plugin-defined commands.** A plugin can contribute CLI commands, invoked as `ra plugin <name> <command>`. The [`et` ra-plugin](https://github.com/adamtait/ra-plugins) provides `ra plugin et connect`, which connects through the local [Eternal Terminal](https://eternalterminal.dev/) client instead of plain SSH so a dropped connection auto-reconnects and resumes the session. It requires the `et` client locally (`brew install et`) and the `et` plugin installed on the workstation (which runs `etserver`). See [ADR 0014](docs/adr/0014-connection-config-and-plugin-commands.md).


## Files

- `main.go` / `cmd/` / `internal/` — `ra` CLI (idempotent provisioning)
- `Makefile` — `make install` builds and drops the binary into `~/.local/bin`
- `internal/scaffold/assets/` — embedded core build assets (Dockerfile, `workstation-startup.d/200_remote-agent-setup.sh`, `profile.d/00-ra-wait.sh`)
- [`ra-plugins`](https://github.com/adamtait/ra-plugins) — the maintained plugins (Claude Code, GitHub, AWS, …), each in its own subdirectory; installed via `ra plugin install <repo> <name>`
- `~/.config/ra/` — your user-global ra config + materialized core assets + installed plugins
- `.ra/` — project-local ra config + (optional) overrides + installed plugins
- `docs/adr/` — architecture decision records ([index](docs/adr/README.md))


## Extension points

There are three ways to customize a workstation, in increasing order of
persistence and reusability. Reach for the lightest one that fits.

| | Where | Persists across rebuilds? | Reusable across |
|---|---|---|---|
| **1. Live changes** | on the running workstation | no | nothing |
| **2. Local config** | `~/.config/ra/` (user) or `.ra/` (project) | yes | all your machines (user) / all machines in the project (project) |
| **3. Plugins** | `ra plugin install <source>` | yes | any user or project with access to the source |

**1. Live changes.** SSH in (`ra connect`) and install or tweak whatever you
like. This is the fastest way to experiment, but the container is ephemeral:
anything outside the persistent home disk is baked from the image, so live
changes are **lost when the workstation is rebuilt** (`ra create`) or deleted.
Use it to try something out, then promote what works into option 2 or 3.

**2. Local configuration extensions.** `~/.config/ra/` (user-level) and `.ra/`
(project-level) mirror the workstation image's `/etc/` layout — drop files into
`Dockerfile.d/`, `workstation-startup.d/`, or `profile.d/`, or override the
`Dockerfile` itself, and they merge into every image build (project shadows user
shadows embedded core). User-level config applies to **every** workstation you
create; project-level config applies to every workstation created from that
project directory (and is committed to source control, so your teammates get it
too). For example, install your personal dotfiles with a user-level
`Dockerfile.d/` hook and every machine you spin up has them. These extensions are
reusable across machines but stay local to you or the project — they aren't
packaged for sharing.

**3. Plugins.** When an extension is worth sharing — it bundles scripts, needs
secrets, or provisions GCP resources — package it as a plugin. Plugins are
installed with `ra plugin install <source>` from a local path or a git repo, so
they're generically reusable by any user or project with access to the source.
See the [`ra plugin` reference](#ra-plugin-reference) below and the
[plugin authoring guide](docs/authoring-plugins.md).


## `ra plugin` reference

```bash
ra plugin install <git-url-or-path>          # plugin.yaml at the repo/dir root
ra plugin install <git-url-or-path> <name>   # plugin in subdirectory <name> of a repo/dir
ra plugin install <source> [<name>] --ref X  # install from git ref X (branch/tag/commit)
ra plugin install <source> [<name>] --user   # force user-level
ra plugin install <source> [<name>] --local  # force project-level
ra plugin install <source> [<name>] --force  # overwrite existing at chosen level

ra plugin list                               # show installed plugins, level, source
ra plugin remove <name>                      # remove a plugin + its config + secret entries
                                             #   (GCP Secret Manager values are NOT auto-deleted;
                                             #    `ra plugin remove` prints the gcloud commands)
```

The two-argument form is how you install one plugin out of a multi-plugin repo
like [`ra-plugins`](https://github.com/adamtait/ra-plugins):

```bash
ra plugin install https://github.com/adamtait/ra-plugins github
ra plugin install https://github.com/adamtait/ra-plugins claude --ref v1.2.0
```

## Creating plugins

`ra` ships with no built-in plugins — every tool and integration is one you
install. Writing your own is straightforward: a plugin is a directory with a
`plugin.yaml` manifest and optional shell scripts that hook into image build,
workstation boot, and login shells.

See **[docs/authoring-plugins.md](docs/authoring-plugins.md)** for the full guide:
what makes a good plugin, where to store one for reuse, the directory layout, the
`plugin.yaml` schema, the runtime env contract, and how to install and test
locally.

## Rotating secrets

All rotations happen via Secret Manager — no need to re-run `ra create`.

For plugins with command-generated credentials, re-run the plugin install command (same source you installed from) to capture and upload a new secret version:

```bash
ra plugin install https://github.com/adamtait/ra-plugins codex --force
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
- Workstation: billed per minute while running. Default `e2-standard-2` ≈ $0.07/hr; bump to `e2-standard-4` (~$0.13/hr) or larger per-project for heavier workloads.
- Auto-stops after 2h idle by default
- Persistent home disk: billed even when stopped (~$0.04/GB-month for pd-standard)

## Security notes

The workstation config carries zero secret values — only secret names and non-sensitive metadata. Anyone with `workstations.configs.get` can read the config and learn *which* secrets are in use, but cannot read the secret values themselves; that requires `secretmanager.secretAccessor`, granted only to the workstation's service account.

To audit credential access, look at Secret Manager's data access audit logs — every fetch (boot-time, per-operation, periodic) appears there.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for
build/test instructions and project conventions, and [SECURITY.md](SECURITY.md)
to report a vulnerability.

## License

[MIT](LICENSE) © Adam Tait
