# Plugin System Design

**Date:** 2026-04-26
**Status:** Approved

## Goal

Replace the compiled-Go plugin system with a file-based plugin format that anyone can write without Go knowledge. Modeled after the Claude Code plugin system: a defined directory structure of simple files covering configuration, secrets, installation, and runtime setup.

---

## Plugin Directory Structure

Each plugin is a directory containing a `plugin.yaml` and optional scripts:

```
<plugin-dir>/<name>/
├── plugin.yaml     # required — metadata, config fields, secrets, auth providers
├── install.sh      # optional — runs at Docker image build time
└── setup.sh        # optional — sourced by entrypoint.sh at container start
```

### Discovery Locations

Plugins are loaded from two locations, in order:

1. `~/.config/ra/plugins/<name>/` — user-global
2. `.ra/plugins/<name>/` — project-local

Project-local plugins shadow user-global plugins of the same name entirely (no merging). At startup `ra` scans both locations and loads every directory containing a `plugin.yaml`.

---

## `plugin.yaml` Schema

```yaml
name: <string>           # required, unique identifier, used as config namespace
version: <string>        # required
description: <string>    # required, shown in ra plugin list

# Config fields — drive ra setup prompts and validation
config:
  - name: <string>
    type: bool | string | int
    description: <string>
    default: <value>       # optional
    required: bool         # default false; error if unset when plugin is active
    options: [<string>]    # optional; presents numbered list in ra setup

# Top-level secrets — always fetched regardless of auth provider
secrets:
  - name: <string>         # GCP Secret Manager secret name
    env_var: <string>      # env var name exported to setup.sh and container
    description: <string>

# Auth providers — optional; present when the plugin supports multiple auth methods
auth_providers:
  - name: <string>
    description: <string>
    secrets:               # additional secrets fetched only when this provider is active
      - name: <string>
        env_var: <string>
        description: <string>
```

### Config namespace

Plugin config is stored in the user/project config YAML under `plugins.<name>.<field>`:

```yaml
plugins:
  claude:
    enabled: true
    auth_provider: oauth
  github:
    enabled: true
    auth_provider: ssh
```

`config.Config` loses its `Claude *ClaudeConfig` field; plugin config is stored as `Plugins map[string]map[string]interface{}`.

---

## Example: Claude Plugin

```yaml
name: claude
version: "1.0.0"
description: "Installs and configures Claude Code on the workstation"

config:
  - name: enabled
    type: bool
    description: "Enable Claude Code?"
    default: false

auth_providers:
  - name: oauth
    description: "OAuth token (recommended)"
    secrets:
      - name: claude_oauth
        env_var: CLAUDE_OAUTH_TOKEN
        description: "OAuth token from claude.ai/settings"

  - name: apikey
    description: "API key via helper script"
    secrets:
      - name: claude_apikey
        env_var: CLAUDE_API_KEY
        description: "API key from console.anthropic.com"
```

---

## Example: GitHub Plugin

```yaml
name: github
version: "1.0.0"
description: "GitHub access for git and gh CLI"

config:
  - name: enabled
    type: bool
    description: "Enable GitHub integration?"
    default: false

secrets:
  - name: github_pat
    env_var: GH_TOKEN
    description: "Personal Access Token — used for gh CLI regardless of git auth method"

auth_providers:
  - name: https
    description: "HTTPS via PAT (simplest, no extra secrets)"

  - name: ssh
    description: "SSH key (separate credential from PAT)"
    secrets:
      - name: github_ssh_key
        env_var: GITHUB_SSH_KEY
        description: "Private SSH key for GitHub"
```

---

## Scripts

### `install.sh`

Runs at Docker image build time during `ra create`. Use for installing packages, binaries, system dependencies.

- Runs as root in the build context
- No secrets or plugin config available
- Must be idempotent

```bash
#!/usr/bin/env bash
npm install -g @anthropic-ai/claude-code
```

### `setup.sh`

Sourced by `entrypoint.sh` at every container start. Use for fetching secrets, writing config files, configuring auth.

Environment available:
- Top-level secrets: exported as their declared `env_var` names (e.g. `GH_TOKEN`)
- Active provider secrets: exported as their declared `env_var` names (e.g. `GITHUB_SSH_KEY`)
- Config values: `RA_PLUGIN_<NAME>_<FIELD>` (uppercased, underscored — e.g. `RA_PLUGIN_GITHUB_ENABLED`, `RA_PLUGIN_GITHUB_AUTH_PROVIDER`)
- Standard workstation vars: `RA_PROJECT_ID`, `RA_WORKSTATION_NAME`

`ra` generates `entrypoint.sh` to source each active plugin's `setup.sh` in load order (user-global first, project-local last).

```bash
#!/usr/bin/env bash
# github setup.sh example

if [ "$RA_PLUGIN_GITHUB_AUTH_PROVIDER" = "https" ]; then
    git config --global credential.helper \
      "!f() { echo username=x; echo password=$GH_TOKEN; }; f"
elif [ "$RA_PLUGIN_GITHUB_AUTH_PROVIDER" = "ssh" ]; then
    mkdir -p ~/.ssh
    echo "$GITHUB_SSH_KEY" > ~/.ssh/id_rsa
    chmod 600 ~/.ssh/id_rsa
    git config --global url."git@github.com:".insteadOf "https://github.com/"
fi
```

---

## `ra plugin` Subcommand

```
ra plugin install <git-url>          # clone into ~/.config/ra/plugins/<name>/
ra plugin install <git-url> --local  # clone into .ra/plugins/<name>/
ra plugin list                       # show all loaded plugins and their source
ra plugin remove <name>              # delete plugin directory
```

`install` clones the repo, reads `plugin.yaml` for the name, places it at the correct path. Errors if a plugin of the same name already exists unless `--force` is passed.

---

## `ra setup` Integration

When `ra setup` runs:

1. Loads all plugins from both locations
2. For each plugin, prompts for each `config` field in order (respecting `default`, `required`, `options`)
3. If the plugin's `enabled` field is false (or the plugin has no `enabled` field), skip steps 4–6 for that plugin
4. If `auth_providers` is non-empty, presents them as a numbered list and prompts the user to choose
5. Prompts for top-level secrets
6. Prompts for the chosen provider's secrets
7. Writes all values to config under `plugins.<name>.*`

---

## `ra validate` Integration

An "active plugin" is one where `plugins.<name>.enabled == true` (or the plugin has no `enabled` field).

Checks for each active plugin:
- All `config` fields with `required: true` are present
- All top-level secrets exist in GCP Secret Manager
- The secrets for the configured `auth_provider` exist in GCP Secret Manager

---

## Go Internals

### Deleted
- `internal/plugins/plugin.go` — `Plugin` interface removed
- `internal/plugins/claude/` — entire directory removed; replaced by file-based plugin

### New
- `internal/plugins/fileplugin.go` — `FilePlugin` struct; reads a plugin directory, implements all plugin behaviors (assets, validation, prompt generation, config fragments, container env pairs)
- `internal/plugins/loader.go` — `LoadAll()` scans both plugin locations; project-local shadows user-global by name; returns `[]*FilePlugin`
- `cmd/plugin.go` — `ra plugin install/list/remove` subcommand

### Modified
- `internal/plugins/registry.go` — simplified to wrap `[]*FilePlugin`; aggregate methods (`AllAssets`, `ValidateAll`, `AllContainerEnvPairs`) unchanged in signature so callers in `cmd/` need no changes
- `internal/config/types.go` — `Claude *ClaudeConfig` replaced by `Plugins map[string]map[string]interface{}`
- `cmd/root.go` — `init()` replaced with `LoadAll()` call; no longer hardcodes `claudeplugin.New()`

### Unchanged
- All callers in `cmd/` that use `reg.AllAssets()`, `reg.ValidateAll()`, `reg.AllContainerEnvPairs()` — registry API is stable
- `internal/scaffold/` — receives registry same as before
- `internal/gcloud/` — unaffected
