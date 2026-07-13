# Extension Points & Plugins

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
[plugin authoring guide](authoring-plugins.md).

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

See **[authoring-plugins.md](authoring-plugins.md)** for the full guide:
what makes a good plugin, where to store one for reuse, the directory layout, the
`plugin.yaml` schema, the runtime env contract, and how to install and test
locally.
