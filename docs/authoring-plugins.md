# Authoring `ra` plugins

A plugin extends an `ra` workstation with a tool, an integration, or a piece of
setup. `ra` itself ships with **no built-in plugins** — everything tool-specific
(Claude Code, the GitHub CLI, AWS, marimo, …) is a plugin you install with
`ra plugin install`. The maintained set lives in
[`ra-plugins`](https://github.com/adamtait/ra-plugins); this guide is for writing
your own.

A plugin is a directory with a `plugin.yaml` manifest and optional shell scripts.
Author one locally and install it with `ra plugin install ./path/to/plugin`.

- [What makes a good plugin](#what-makes-a-good-plugin)
- [Where to store plugins](#where-to-store-plugins)
- [Directory structure](#directory-structure)
- [Script naming conventions](#script-naming-conventions)
- [Putting a CLI on PATH](#putting-a-cli-on-path)
- [`plugin.yaml` schema](#pluginyaml-schema)
- [Runtime environment contract](#runtime-environment-contract)
- [Standard startup script preamble](#standard-startup-script-preamble)
- [First-boot markers](#first-boot-markers)
- [Tips](#tips)
- [Installing and testing locally](#installing-and-testing-locally)

For the design rationale behind the file-based plugin system, see
[ADR 0007](adr/0007-file-based-plugin-system.md) and
[ADR 0008](adr/0008-zero-builtin-plugin-image-mirror-architecture.md).


## What makes a good plugin

Not every bit of setup needs to be a plugin. A one-off `apt install` you only
ever want in one project is fine to keep in that project's `.ra/Dockerfile`. Reach
for a plugin when the setup has one of these shapes — the things `ra`'s
infrastructure exists to handle:

- **It needs secrets.** Any tool that authenticates with an API key, OAuth token,
  service-account key, or PAT. The plugin declares the secret; `ra` captures the
  value at install time, stores it in GCP Secret Manager, and the workstation's
  service account fetches it fresh at every boot. Credentials never touch config
  or the image. Examples: `claude`, `github`, `aws`, `gcloud`, `codex`.

- **It needs gcloud resources.** Any tool that requires a GCP resource provisioned
  and wired up — a GCS bucket for state, an exposed workstation port for a web
  server, a stable per-workstation namespace. The plugin *declares* the resource
  (`buckets:`, `port-for-tunnel:`, `stable_id:`) and `ra` provisions it and hands
  it to the runtime via env var, with no plugin-specific code in `ra` core.
  Examples: `claude-sync` (a GCS bucket), `marimo` (a tunnelled port).

- **It needs to bake into the container image.** Any tool that must be installed
  at Docker build time — a CLI binary, a language runtime, a system package —
  rather than fetched per-boot. The plugin drops a `Dockerfile.d/` hook that
  merges into the image build. Examples: `babashka`, `uv`, `terraform`, `tmux`.

A plugin can combine all three (install a binary, authenticate it with a secret,
provision a bucket for its state). If a piece of setup needs **none** of them —
no secret, no GCP resource, no image change — it probably doesn't need to be a
plugin at all.


## Where to store plugins

A plugin's source can be any local path or git repository, so you have three
common options for keeping one around and reusing it:

1. **In the same repo as your project** — commit the plugin under your project (a
   `plugins/my-plugin/` directory, say) and install it by relative path:

   ```bash
   ra plugin install ./plugins/my-plugin --local
   ```

   Best for a plugin that only makes sense for this one project and should be
   versioned alongside it. `--local` installs it at the project level (`.ra/`).

2. **In another local directory** — keep a plugin (or a directory of plugins) you
   reuse across projects somewhere on your machine and install by absolute path:

   ```bash
   ra plugin install ~/dev/ra-plugins-mine/my-plugin --user
   ```

   Good while you're still iterating on a plugin you haven't published yet.

3. **In a GitHub repo (public or private)** — publish the plugin to its own repo,
   or a monorepo with one subdirectory per plugin (the pattern
   [`ra-plugins`](https://github.com/adamtait/ra-plugins) uses), and install by URL:

   ```bash
   ra plugin install https://github.com/you/my-ra-plugins my-plugin
   ra plugin install https://github.com/you/my-ra-plugins my-plugin --ref v1.2.0
   ```

   Best for sharing across a team or machines. The second (optional) argument
   selects a subdirectory of the repo; `--ref` pins a branch, tag, or commit.
   **Private repos work** — `ra` clones with your local git credentials, so any
   repo your `git`/`gh` setup can already reach is installable.

Whichever you choose, `ra plugin install` records where each plugin came from
(written into `<plugin-dir>/.ra-install.yaml`), `ra plugin list` shows it, and
re-running the same install command (add `--force`) re-syncs from the source.


## Directory structure

```
my-plugin/
├── plugin.yaml                          # required
├── Dockerfile.d/
│   └── 100_my-plugin-install.sh         # optional: build-time hooks
├── workstation-startup.d/
│   └── 250_my-plugin-setup.sh           # optional: per-boot runtime setup
└── profile.d/
    └── 10-my-plugin-env.sh              # optional: login-shell additions
```

Plugin scripts mirror where they land in the image (`/etc/...`):

| Directory | When it runs | Use it for |
|---|---|---|
| `Dockerfile.d/` | Docker image build time | Installing packages, downloading binaries |
| `workstation-startup.d/` | Every container boot, after `200_remote-agent-setup.sh` | Auth setup, credential helpers, first-boot data clones |
| `profile.d/` | Every **bash / POSIX-`sh`** login shell | Shell aliases, login-shell setup. **Not sourced by zsh** — don't put PATH here; see [Putting a CLI on PATH](#putting-a-cli-on-path) |


## Script naming conventions

Filenames follow `<NNN>_<plugin-name>-<purpose>.sh`. Numbers control execution
order within the directory. Scripts from all installed plugins merge into one
directory per stage and run in sorted order.

- **`Dockerfile.d/`** — use `100`–`199`. Merged and run in sorted order during
  `docker build`.
- **`workstation-startup.d/`** — use `250`+. The core `200_remote-agent-setup.sh`
  runs first and writes fetched secrets to `/run/ra/env`; plugin scripts must be
  numbered ≥ 250 to guarantee secrets are available.
- **`profile.d/`** — use `10`+ for plugin additions (`00`–`09` is reserved for the
  core `00-ra-wait.sh`, which blocks the login shell until the entrypoint has
  finished). Lower numbers run first.


## Putting a CLI on PATH

Install executables into **`/usr/local/bin`** — it's on the baseline PATH of every
shell (bash, zsh, …) and every invocation type, so nothing needs to touch shell
config. Do **not** add a directory to PATH via `profile.d/`: those files are not
sourced by zsh, so the entry silently disappears whenever the login shell is zsh.

If a plugin installs a binary somewhere else at runtime (e.g. a first-boot
`npm install -g` into `~/.npm-global`, or an installer that drops into
`~/.local/bin`), bake a build-time symlink from `/usr/local/bin` into the image in
a `Dockerfile.d/` hook rather than editing PATH. The `claude` and `codex` plugins
in [`ra-plugins`](https://github.com/adamtait/ra-plugins) do exactly this — see
[`claude/Dockerfile.d/110_claude-path-link.sh`](https://github.com/adamtait/ra-plugins/blob/main/claude/Dockerfile.d/110_claude-path-link.sh)
and the rationale in
[ra-plugins ADR 0002](https://github.com/adamtait/ra-plugins/blob/main/docs/adr/0002-plugin-clis-on-path-across-login-shells.md).


## `plugin.yaml` schema

```yaml
name: my-plugin           # required; becomes RA_PLUGIN_<NAME>_* prefix
version: "1.0.0"
description: "What this plugin does"

config:
  - name: enabled         # include in every plugin
    type: bool            # bool | string | string_list | int
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
    value_command:                         # optional: capture at install time
      command: my-cli
      args: ["login"]
      output: stdout                       # stdout | file_base64
      path: "~/.my-cli/auth.json"          # required for file_base64
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

`ra plugin install` prompts for each `config` field, the chosen `auth_provider`
(if any), and secret values (hidden input). If a field or secret declares
`value_command`, `ra` runs that local command during install and uses its output
instead of prompting. Config is written to `config.yaml`; secret *values* go to
GCP Secret Manager (only names and metadata are stored in config).

**`string_list` values** are pipe-joined into a single env var, so a list value
may not itself contain `|`.

### Additional top-level fields

- **`port-for-tunnel: <port>`** — advertises a single in-workstation TCP port. `ra`
  renders it as `plugins.<name>.port-for-tunnel` in config (user-overridable) and
  `ra tunnel <plugin-name>` opens a local tunnel to it. Used by `marimo`. See
  [ADR 0001](adr/0001-ra-tunnel-and-plugin-port-field.md).
- **`buckets:`** — declares a GCS bucket that `ra` provisions at `ra create` time
  and exposes at runtime via env var. Fields: `name`, `name_template`,
  `override_field`, `role`, `env_var`, plus optional `lifecycle` rules. Used by
  `claude-sync`. See [ADR 0002](adr/0002-claude-state-sync.md).
- **`commands:`** — declares CLI subcommands the plugin contributes, invoked as
  `ra plugin <plugin-name> <command-name>`. Each runs a local `run:` script with a
  workstation env contract. Used by `et` (`ra plugin et connect`). See
  [ADR 0014](adr/0014-connection-config-and-plugin-commands.md).
- **`post_install_message:`** — a message `ra plugin install` prints after a
  successful install (e.g. next steps, a client the user must install locally).

`ra` core hard-codes none of these plugin names — it walks all installed plugins
generically, so declaring a resource is all it takes to have it provisioned.


## Runtime environment contract

`ra create` injects plugin configuration as container env vars:

```
RA_PLUGIN_<UPPERCASE_NAME>_<UPPERCASE_FIELD>=<value>   # config fields
RA_PLUGIN_<UPPERCASE_NAME>_AUTH_PROVIDER=<provider>    # if auth_providers used
RA_SECRET_<UPPERCASE_LOGICAL>_NAME=<gcp-secret-name>   # secret metadata
RA_SECRET_<UPPERCASE_LOGICAL>_VAR=<env-var-name>
```

Secret *values* are fetched from GCP Secret Manager at each boot by
`200_remote-agent-setup.sh` and written to `/run/ra/env`. Your startup script
reads them by sourcing that file.

**`Dockerfile.d/` scripts do not have access to `RA_PLUGIN_*` env vars.** They run
at image build time, as root, with no user home, no secrets, and no runtime env.
Build-time behavior (e.g. which binary version to install) must be hardcoded in
the script.


## Standard startup script preamble

Every `workstation-startup.d/` script should open with:

```bash
[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a
[ "${RA_PLUGIN_MY_PLUGIN_ENABLED:-false}" = "true" ] || exit 0
```

Line 1 sources fetched secrets. Line 2 exits immediately if the plugin is
disabled — without this gate, your script runs on every workstation even when the
plugin is not installed by that user.

Scripts in `profile.d/` do **not** need to source `/run/ra/env` themselves — the
core `00-ra-wait.sh` waits for the entrypoint sentinel and sources the env before
any later `profile.d/` script runs. Gate them on `RA_PLUGIN_<NAME>_ENABLED` just
the same.


## First-boot markers

For work that should run only once per workstation lifetime (cloning a repo,
expensive initial setup):

```bash
FIRST_BOOT_MARKER="/home/user/.ra-my-plugin-first-boot-done"
[ -e "${FIRST_BOOT_MARKER}" ] && exit 0

# ... first-boot work ...

touch "${FIRST_BOOT_MARKER}"
chown user:user "${FIRST_BOOT_MARKER}"
```


## Tips

- **Always gate on `RA_PLUGIN_<NAME>_ENABLED`.** Without it, your script runs on
  every workstation even when the plugin is not installed by that user.
- **Every-boot scripts must be idempotent.** They run on every container start.
  Overwrite config files (`>`), don't append (`>>`).
- **`Dockerfile.d/` scripts run as root** with no user home, no secrets, and no
  runtime env. Keep them to system-level installs only.
- **Don't write `/etc/environment` or `/run/ra/env` by hand.** Use the shared
  `ra_env_set KEY VALUE` / `ra_env_unset KEY` helpers from
  `/usr/local/lib/ra/ra-env.sh` (source it first) — they dedup and update both
  files consistently.


## Installing and testing locally

```bash
ra plugin install ./my-plugin --user            # install from local path
ra plugin list                                  # confirm installed
ra create                                       # rebuild image + reprovision

ra plugin remove my-plugin                      # remove when done
```

To test a startup script on a running workstation without a full rebuild:

```bash
ra connect
sudo bash /etc/workstation-startup.d/250_my-plugin-setup.sh
```
