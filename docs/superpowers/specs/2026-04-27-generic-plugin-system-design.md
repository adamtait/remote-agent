# Generic Plugin System Design

**Date:** 2026-04-27
**Status:** Approved

## Goal

Make `ra`'s on-disk layout and plugin system fully generic so the binary ships with zero built-in plugins. The user-global, project-local, and final-image paths all mirror each other, and every claude-specific line is removed from the core Dockerfile and entrypoint. The claude plugin is moved to a top-level directory in the repo to prepare for extraction into a separate git repo.

After this change:
- `ra setup` configures only ra itself — no plugin prompting, no plugin asset materialization
- `ra plugin install <git-url-or-path>` is the only way to add a plugin: it clones, prompts, uploads secret values to GCP Secret Manager, and writes config
- The on-disk layout under `~/.config/ra/` and `.ra/` mirrors the workstation image's `/etc/` layout
- A plugin's directory layout mirrors where its scripts land in the final image (`workstation-startup.d/`, `profile.d/`, `Dockerfile.d/`)

---

## Source-of-truth layout

### User-global (`~/.config/ra/`) and project-local (`.ra/`)

Both directories mirror each other and mirror the image's `/etc/` paths:

```
{~/.config/ra,.ra}/
├── config.yaml
├── Dockerfile
├── Dockerfile.d/                                # build-time *.sh hooks
├── workstation-startup.d/
│   └── 200_remote-agent-setup.sh
├── profile.d/
│   └── 00-ra-wait.sh
└── plugins/
    └── <name>/...
```

The `template/` subdirectory under `~/.config/ra/` is removed. The `.ra/profile.d.staged/` directory is removed. Project-local `.ra/.build/` is the ephemeral docker build context (gitignored).

### Plugin directory

A plugin's directory mirrors the image destinations its scripts land in:

```
<plugin>/<name>/
├── plugin.yaml                                  # required: schema, config, secrets
├── Dockerfile.d/                                # optional: build-time *.sh
│   └── <NNN>_<name>-<purpose>.sh
├── workstation-startup.d/                       # optional: runtime startup *.sh
│   └── <NNN>_<name>-<purpose>.sh                # → /etc/workstation-startup.d/<file>
└── profile.d/                                   # optional: login-shell *.sh
    └── <NNN>_<name>.sh                          # → /etc/profile.d/<file>
```

There is no longer an `install.sh` or `setup.sh`. Every plugin script is a numbered drop-in to one of the three image-mirror directories. First-boot install is a `workstation-startup.d/` script with an inline first-boot marker check.

### Claude plugin (in-tree, parallel to `internal/`)

```
plugins/claude/
├── plugin.yaml
├── Dockerfile.d/
│   └── 100_claude-nodejs.sh                     # apt install nodejs (was in core Dockerfile)
├── workstation-startup.d/
│   ├── 250_claude-install.sh                    # first-boot npm install (gated by marker)
│   └── 260_claude-setup.sh                      # every-boot auth config
└── profile.d/
    └── 00-claude-npm-path.sh                    # PATH for npm-installed claude binary
```

This top-level location is parallel to `internal/`, `cmd/`, `docs/`. A subsequent changeset will `git mv plugins/claude/` into a separate repo; this design only prepares for that move.

---

## Build context assembly

`ra create` assembles a fresh `.ra/.build/` directory each run, runs `docker buildx build` from there, and deletes the directory on exit (success or failure).

### Resolution rules

| Target in `.ra/.build/` | Sources | Merge rule |
|---|---|---|
| `Dockerfile` | `.ra/Dockerfile` → `~/.config/ra/Dockerfile` → embedded | First match wins; single file |
| `Dockerfile.d/` | `.ra/Dockerfile.d/` ⊕ `~/.config/ra/Dockerfile.d/` ⊕ each plugin's `Dockerfile.d/` | Filename-level merge; project > user > plugins; sort by filename for build order |
| `workstation-startup.d/` | `.ra/workstation-startup.d/` ⊕ `~/.config/ra/workstation-startup.d/` ⊕ each plugin's `workstation-startup.d/` | Same rule |
| `profile.d/` | `.ra/profile.d/` ⊕ `~/.config/ra/profile.d/` ⊕ each plugin's `profile.d/` | Same rule |
| `plugins/<name>/` | `.ra/plugins/<name>/` → `~/.config/ra/plugins/<name>/` | Whole-plugin granularity; project shadows user; no in-tree defaults |

Plugins are staged twice: once into `plugins/<name>/...` (so `/etc/ra/plugins/<name>/` carries the original tree for inspection) and once into the merged top-level `Dockerfile.d/`, `workstation-startup.d/`, `profile.d/` (where they actually run). The plugin tree at `/etc/ra/plugins/` is informational only.

When staging into `plugins/<name>/...`, the assembler skips the `.ra-install.yaml` metadata file — that file is `ra plugin install` bookkeeping and has no place in the image.

### Empty-dir handling

If a stage directory under `.ra/.build/` would be empty (e.g. no plugins installed, no `Dockerfile.d/` contributions), the assembler creates it and writes a single empty file `.ra-keep` inside, so the Dockerfile's `COPY <dir>/ ...` succeeds. The `.ra-keep` files land at `/etc/profile.d/.ra-keep`, `/etc/workstation-startup.d/.ra-keep`, etc. — harmless because the chmod globs (`*.sh`) skip them and login-shell loaders skip dotfiles.

### Build flow

```
1. mkdir -p .ra/.build/
2. Resolve Dockerfile → .ra/.build/Dockerfile
3. Merge */Dockerfile.d/ → .ra/.build/Dockerfile.d/
4. Merge */workstation-startup.d/ → .ra/.build/workstation-startup.d/
5. Merge */profile.d/ → .ra/.build/profile.d/
6. Merge plugins (whole-plugin) → .ra/.build/plugins/<name>/
7. docker buildx build -f .ra/.build/Dockerfile .ra/.build/
8. defer: rm -rf .ra/.build/
```

---

## Dockerfile

The embedded default `Dockerfile` is fully generic. Paths inside the file are relative to the build context root (`.ra/.build/`), so no `.ra/` prefix appears.

```dockerfile
FROM us-central1-docker.pkg.dev/cloud-workstations-images/predefined/code-oss:latest

USER root

# --- Core system packages ---------------------------------------------------
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates gnupg git git-lfs jq \
    && git lfs install --system \
    && rm -rf /var/lib/apt/lists/*

# --- GitHub CLI -------------------------------------------------------------
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)

# --- Plugin build-time hooks ------------------------------------------------
COPY Dockerfile.d/ /tmp/ra-dockerfile-d/
RUN set -e; \
    for s in $(find /tmp/ra-dockerfile-d -name '*.sh' -type f | sort); do \
        echo "==> running build hook: $s"; \
        bash "$s"; \
    done; \
    rm -rf /tmp/ra-dockerfile-d

# --- Plugin tree (informational; live paths are below) ----------------------
COPY plugins/ /etc/ra/plugins/
RUN find /etc/ra/plugins -type f -name '*.sh' -exec chmod 0755 {} +

# --- Workstation-startup scripts (merged: ra core + plugins) ----------------
COPY workstation-startup.d/ /etc/workstation-startup.d/
RUN chmod 0755 /etc/workstation-startup.d/*.sh

# --- Login-shell profile.d scripts (merged: ra core + plugins) --------------
COPY profile.d/ /etc/profile.d/
RUN chmod 0644 /etc/profile.d/*.sh
```

### Removed from the previous Dockerfile
- `RUN curl ... nodejs` block → moves to `plugins/claude/Dockerfile.d/100_claude-nodejs.sh`
- `RUN printf '...00-ra-npm-global-path.sh'` block → moves to `plugins/claude/profile.d/00-claude-npm-path.sh`
- `mkdir -p /etc/ra/plugins` + `find ... chmod` → replaced by generic per-merged-dir chmods above
- `COPY .ra/entrypoint.sh /etc/workstation-startup.d/200_ra-setup.sh` → replaced by directory `COPY workstation-startup.d/`
- `COPY .ra/profile.d.staged/ /etc/profile.d/` → replaced by directory `COPY profile.d/` from the build context

### `docker buildx` invocation in `cmd/create.go`

```go
"buildx", "build",
"--platform", "linux/amd64",
"-t", imageURI,
"-f", ".ra/.build/Dockerfile",
"--push",
".ra/.build",
```

---

## Plugin lifecycle

### `ra setup` (slimmed down)

1. Probe gcloud/git defaults
2. Prompt for `project_id`, `github_repo_url`, `git_user_name`, `git_user_email`
3. Write `~/.config/ra/config.yaml` and/or `.ra/config.yaml` with **no `plugins:` section**
4. Materialize core assets to `~/.config/ra/`:
   - `Dockerfile`
   - `workstation-startup.d/200_remote-agent-setup.sh`
   - `profile.d/00-ra-wait.sh`

   And to `.ra/` when `--with-project-assets` is passed.

Removed from `ra setup`:
- Plugin prompt loop (`for _, p := range opts.Registry.All() { p.Prompt(...) }`)
- Plugin asset materialization (`reg.AllAssets()` extras)

The hardcoded `github_pat:` secrets block in `RenderUserConfig` stays for now; see "Github extraction prep" below.

### `ra plugin install <source>`

`<source>` is a git URL or a local filesystem path. Local paths support plugin authors developing locally and the in-tree → external repo transition for claude.

Flow:
1. **Acquire**: clone (git URL) or `cp -r` (local path) into a temp dir.
2. **Validate**: parse `plugin.yaml`; verify required fields. After step 3 chooses the install level, verify the plugin name is unique *at that level* (the same plugin existing at a different level is fine — project shadows user by design); error unless `--force`.
3. **Choose level** (interactive prompt unless `--user` or `--local` flag is given):
   ```
   Install at user level (~/.config/ra/plugins/) or project level (.ra/plugins/)?
     1) user
     2) project
   choice [1-2] [1]: _
   ```
4. **Move**: temp dir → `<chosen-base>/plugins/<name>/`.
5. **Prompt config fields**, **auth_provider** (if applicable), and **secret values** (hidden input via `term.ReadPassword`).

   `FilePlugin.Prompt` is reworked from its current return signature `(configBlock, secretsBlock string, err error)` (pre-baked YAML strings) to a structured result:

   ```go
   type PromptResult struct {
       Config       map[string]any           // field name → value
       AuthProvider *AuthProvider            // nil if plugin has none
       SecretValues map[string]string        // logical secret name → plaintext value
   }
   ```

   The caller (`cmd/plugin.go`) uses this directly to upload secrets in step 6 and to render YAML fragments for the config write in step 7. Existing prompt UX (defaults, options, hidden secret input) is unchanged; only the return shape moves.
6. **Upload secrets to GCP Secret Manager** — requires gcloud auth + `gcloud.project` from config; if config doesn't exist, error: `"run 'ra setup' first"`. Reuses `ensureSecret` (extracted from `cmd/create.go` to `internal/secrets/upload.go`).
7. **Write config**: append `plugins.<name>.*` and `secrets.<logical>: {secret_name, env_var}` blocks to the chosen-level `config.yaml`. Preserves existing config (YAML merge, not overwrite).
8. **Write install metadata** to `<plugin-dir>/.ra-install.yaml`:
    ```yaml
    source: <git-url-or-local-path>
    installed_at: 2026-04-27T15:04:05Z
    ```

Errors mid-flow leave the plugin dir in place but warn the user to retry. Re-running with `--force` clobbers cleanly.

Edge cases:
- **`ra plugin install` before `ra setup`**: error with clear message.
- **Plugin without secrets**: skip steps 7–8.
- **Plugin with config but no secrets**: prompt config + write `plugins.<name>` block, no secrets work.

### `ra plugin list`

Reads each installed plugin's `.ra-install.yaml` and prints a table:

```
NAME       LEVEL    SOURCE                                            DESCRIPTION
claude     user     https://github.com/adamtait/ra-claude-plugin     Installs and configures Claude Code
foo        project  /home/user/dev/my-foo-plugin                     Foo-doer
```

If a plugin is shadowed by another level, mark it explicitly: `(shadowed by project)`.

### `ra plugin remove <name>`

1. Search `~/.config/ra/plugins/<name>/` and `.ra/plugins/<name>/`. Error if neither exists; prompt-to-choose if both.
2. Delete the chosen plugin dir.
3. Remove the `plugins.<name>.*` block from that level's `config.yaml`.
4. Remove the plugin's secrets mappings from the `secrets:` block (only the ones declared in `plugin.yaml`).
5. Do **not** delete GCP Secret Manager values. Print them and tell the user to delete manually if they want (`gcloud secrets delete <name>`).

---

## Code changes

### Files moved
- `internal/plugins/defaults/claude/` → `<repo>/plugins/claude/`
  - `plugin.yaml` — unchanged
  - `install.sh` content split into:
    - `Dockerfile.d/100_claude-nodejs.sh` (build-time apt install nodejs)
    - `workstation-startup.d/250_claude-install.sh` (first-boot npm install with marker check)
  - `setup.sh` → `workstation-startup.d/260_claude-setup.sh`
    - Add preamble: `[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a`
    - Add gating: `[ "${RA_PLUGIN_CLAUDE_ENABLED:-false}" = "true" ] || exit 0`
    - First-boot install logic moves out (now its own script at 250)
  - New `profile.d/00-claude-npm-path.sh` (the line removed from the core Dockerfile)
- `internal/scaffold/assets/entrypoint.sh` → `internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh`

### Files deleted
- `internal/plugins/defaults/` (entire directory)
- `internal/plugins/defaults_embed.go`
- `internal/scaffold/scaffold_assets.go` (`ScaffoldAssets`, `AssetResult`, `assetMode`)
- `internal/scaffold/scaffold_assets_test.go`
- `internal/scaffold/stage_profile_d.go`
- `internal/scaffold/stage_profile_d_test.go`
- `internal/scaffold/source.go` (replaced by `buildcontext.go`)
- `internal/scaffold/source_test.go`

### Files added
- `internal/scaffold/buildcontext.go` — assembles `.ra/.build/` per the rules above; returns the build-context path + cleanup func.
- `internal/scaffold/buildcontext_test.go`
- `internal/secrets/upload.go` — extracted from `cmd/create.go`'s `ensureSecret`; reused by `ra plugin install`.
- `internal/secrets/upload_test.go`

### Files modified
- `internal/plugins/fileplugin.go`:
  - `Assets()` removed; the buildcontext assembler walks `<plugin-dir>/{Dockerfile.d,workstation-startup.d,profile.d}` directly.
  - `Prompt()` return shape changes to `PromptResult` (struct) per the lifecycle section.
  - New helper to render YAML fragments from a `PromptResult` for the config write step.
- `internal/plugins/loader.go` — drop the embedded-defaults loading branch (no more in-tree defaults).
- `internal/scaffold/scaffold.go` — `InteractiveScaffold` and `TemplateScaffold` drop the plugin-prompt loop and plugin asset materialization; write core assets to new paths.
- `internal/scaffold/templates.go` — `RenderUserConfig` no longer interpolates `PluginFragments`; remove the `PluginFragments` field from `UserValues`.
- `cmd/create.go` — replace `materializeBuildAssets` + `StageProfileD` with `scaffold.AssembleBuildContext`; update `dockerBuildArgs` to point at `.ra/.build/Dockerfile` with build context `.ra/.build`.
- `cmd/setup.go` — drop plugin-prompt code.
- `cmd/plugin.go` — install flow extended per the lifecycle section above; `list` reads `.ra-install.yaml` for source URL; `remove` cleans up config + secrets.
- `internal/plugins/registry.go` — drop `AllAssets()`; add `WalkContributedDirs` (or remove the registry entirely if buildcontext walks plugin dirs directly).
- `.gitignore` — add `.ra/.build/`.

### Files unchanged
- `internal/gcloud/`
- `internal/config/types.go` (the `Plugins map[string]map[string]any` is already in place)
- `cmd/connect.go`, `cmd/list.go`, `cmd/delete.go`, `cmd/stop.go`, `cmd/logs.go`, `cmd/url.go`, `cmd/resolve.go`

---

## Github extraction prep (light refactoring)

Not strictly required, but cheap moves that ease the future github plugin extraction:

1. Extract the `github_pat` dispatcher in `200_remote-agent-setup.sh` into a function (`_ra_handle_github_pat()`) inside the file. Currently inlined in the `case "${logical}" in github_pat) ... ;;` block. Wrapping makes it a one-line move into a future `plugins/github/workstation-startup.d/255_github-pat.sh`.
2. Pull the hardcoded `github_pat:` block out of `RenderUserConfig` into a named constant `defaultGithubPatSecretsBlock`. Marks the seam clearly so the github plugin extraction is "delete this constant + remove its interpolation point."
3. Pull the `git config user.name/user.email` + `git clone` block in the entrypoint (lines 145–164) into a function `_ra_first_boot_git()`.

No semantics change. Pure code organization to make the next changeset smaller.

---

## Documentation updates

- `CLAUDE.md` — update to reflect:
  - `ra` ships with no built-in plugins
  - The new on-disk layout (no `template/`, no `.staged/`)
  - The `Dockerfile.d/` / `workstation-startup.d/` / `profile.d/` plugin contribution model
  - `.ra/.build/` is ephemeral and gitignored
- `README.md` — update the "getting started" flow:
  - `ra setup`
  - `ra plugin install <claude-plugin-source>` (interactive level prompt)
  - `ra create`
- Plugin authoring guide (new section in `CLAUDE.md` or new file) — describe the `plugin.yaml` schema and the three contribution directories.
