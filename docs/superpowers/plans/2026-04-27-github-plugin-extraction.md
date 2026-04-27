# GitHub Plugin Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move every GitHub-specific concern (the `gh` CLI install, the `github_pat` secret special-casing, first-boot repo cloning, the `git.github_repo_url` config field, and SSH→HTTPS URL normalization) out of `ra` core and into a new in-tree plugin at `plugins/github/`, so `ra` itself ships truly plugin-agnostic and a workstation only does GitHub things if the user runs `ra plugin install ./plugins/github`.

**Architecture:** Build the plugin first (additive — does not break the existing build), then strip the corresponding GitHub bits from core in atomic commits that keep `go test ./...` green at every step. Generic git identity (`git.user_name`, `git.user_email`) stays in core; only GitHub-flavored bits move. Repo cloning becomes the github plugin's responsibility (it reads `RA_PLUGIN_GITHUB_REPO_URL` from container env at first boot).

**Tech Stack:** Go 1.22+ (`gopkg.in/yaml.v3`, `cobra`), bash entrypoint scripts, Docker buildx, GCP Secret Manager via shelled-out `gcloud`. Plugin schema: `internal/plugins/schema.go`. Build-context assembler: `internal/scaffold/buildcontext.go`. Plugin install command: `cmd/plugin.go`.

---

## File Structure

**New files** (created in Phase 1, all under `plugins/github/`):

| Path | Responsibility |
|------|----------------|
| `plugins/github/plugin.yaml` | Plugin schema: `enabled`, `repo_url` config; `pat` auth provider with `github_pat`/`GH_TOKEN` secret. |
| `plugins/github/Dockerfile.d/100_github-cli.sh` | Build-time install of `gh` (the GitHub CLI) into the image. |
| `plugins/github/workstation-startup.d/250_github-credential-helper.sh` | Every-boot install of git credential helper that fetches the PAT fresh from Secret Manager per-git-op. |
| `plugins/github/workstation-startup.d/260_github-clone.sh` | First-boot setup of git identity + clone of `RA_PLUGIN_GITHUB_REPO_URL` into `/home/user/workspace`. Inline SSH→HTTPS normalization. |

**Modified files** (Phases 2–5, listed in order they get touched):

| Path | What changes |
|------|--------------|
| `internal/scaffold/assets/Dockerfile` | Strip the `gh` install block. Keep curl/git/git-lfs/jq/etc. |
| `internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh` | Drop `_ra_handle_github_pat`, the `github_pat` dispatch case, and `_ra_first_boot_git`. |
| `internal/config/types.go` | Drop `GithubRepoURL` from `GitConfig`. |
| `internal/config/merge.go` | Drop `GithubRepoURL` lift. |
| `internal/config/validate.go` | Drop `git.github_repo_url` and `secrets.github_pat` requirements; drop `GITHUB_REPO_URL` from `reservedEnvVars`. |
| `internal/scaffold/templates.go` | Drop `defaultGithubPatSecretsBlock`, `GithubRepoURL` fields, `placeholderRepoURL`, references in render funcs. |
| `internal/scaffold/scaffold.go` | Drop `github_repo_url` prompt + SSH→HTTPS warn. |
| `internal/scaffold/validate.go` | Delete `sshGithubRe`, `ValidateGithubRepoURL`, `NormalizeGithubRepoURL`. |
| `cmd/create.go` | Drop `cfg.Git.GithubRepoURL` from `buildContainerEnv`, the SSH normalization block, the `,=` validation entry, and `git.github_repo_url` from the cmd `Long` description. |
| `internal/config/validate_test.go` | Drop `github_pat` and `github_repo_url` test cases; drop `GH_TOKEN`/`GITHUB_REPO_URL` reservation tests. |
| `internal/config/merge_test.go` | Drop `GithubRepoURL` references in seeds; drop `TestMerge_GitRepoURL_UserDefaultProjectOverride`. |
| `internal/scaffold/templates_test.go` | Drop github_repo_url and github_pat assertions. |
| `internal/scaffold/scaffold_test.go` | Drop the github_repo_url prompt input from every interactive test. |
| `internal/scaffold/validate_test.go` | Delete `TestValidateGithubRepoURL` and `TestNormalizeGithubRepoURL`. |
| `cmd/create_test.go` | Drop `github_pat`, `GH_TOKEN`, `GITHUB_REPO_URL`, `cfg.Git.GithubRepoURL` from `TestBuildContainerEnv_IncludesSecretsAndEnv`. |
| `cmd/setup_test.go` | Drop the `https://github.com/a/b.git` line from `TestSetupInteractive_*` stdin inputs. |
| `README.md` | Update setup walkthrough; remove the "GitHub repo URL" prompt step from `ra setup`'s description. |
| `CLAUDE.md` | Update the "Subcommands → Core Packages" and "Zero-Secrets Design" wording to remove `github_pat` special-casing. |

---

## Phase 1 — Build the github plugin (additive)

These tasks do not touch core code. After Phase 1 the build is unchanged; nothing references the new plugin yet.

### Task 1: Create `plugins/github/plugin.yaml`

**Files:**
- Create: `plugins/github/plugin.yaml`

- [ ] **Step 1: Create directory + plugin.yaml**

```bash
mkdir -p plugins/github/Dockerfile.d plugins/github/workstation-startup.d
```

Write `plugins/github/plugin.yaml`:

```yaml
name: github
version: "1.0.0"
description: "Installs the GitHub CLI and a PAT-based git credential helper; optionally clones a repo on first boot"
config:
  - name: enabled
    type: bool
    description: "Enable GitHub plugin?"
    default: true
  - name: repo_url
    type: string
    description: "Git repository to clone into ~/workspace on first boot (https://, git@github.com:owner/repo, or empty to skip clone)"
    default: ""
auth_providers:
  - name: pat
    description: "Personal Access Token (rotation auto-picked-up via git credential helper)"
    secrets:
      - name: github_pat
        env_var: GH_TOKEN
        description: "GitHub Personal Access Token (classic or fine-grained, repo:read+write)"
        default_secret_name: github-pat
```

- [ ] **Step 2: Verify the plugin parses**

Run:

```bash
go test ./internal/plugins/...
```

Expected: PASS. The plugins package's tests load any plugin via `LoadPlugin(fs.FS)` — if the YAML is malformed the test would catch it. (Note: there's no test that auto-loads `plugins/github/`, but `go vet` and `go build` ensure embedded plugin paths still compile.)

- [ ] **Step 3: Commit**

```bash
git add plugins/github/plugin.yaml
git commit -m "feat(plugin/github): add plugin.yaml with PAT auth provider"
```

---

### Task 2: Add `Dockerfile.d/100_github-cli.sh` (gh CLI install)

**Files:**
- Create: `plugins/github/Dockerfile.d/100_github-cli.sh`

- [ ] **Step 1: Write the script**

Write `plugins/github/Dockerfile.d/100_github-cli.sh`:

```bash
#!/bin/bash
# GitHub plugin — install the GitHub CLI (`gh`) at image build time.
# Equivalent to the block previously hardcoded in the core ra Dockerfile.
set -euo pipefail

curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list
apt-get update
apt-get install -y --no-install-recommends gh
rm -rf /var/lib/apt/lists/*
```

- [ ] **Step 2: Make it executable, verify**

```bash
chmod +x plugins/github/Dockerfile.d/100_github-cli.sh
test -x plugins/github/Dockerfile.d/100_github-cli.sh && echo OK
```

Expected: `OK`.

- [ ] **Step 3: Commit**

```bash
git add plugins/github/Dockerfile.d/100_github-cli.sh
git commit -m "feat(plugin/github): add Dockerfile.d hook to install gh CLI"
```

---

### Task 3: Add `workstation-startup.d/250_github-credential-helper.sh`

This script extracts `_ra_handle_github_pat()` from the core entrypoint into the plugin. It runs every boot, reads `RA_SECRET_GITHUB_PAT_NAME` and `GCP_PROJECT_ID` from the environment, and writes `/usr/local/bin/git-credential-gcp-secret` + `git config --system credential.helper`.

**Files:**
- Create: `plugins/github/workstation-startup.d/250_github-credential-helper.sh`

- [ ] **Step 1: Write the script**

Write `plugins/github/workstation-startup.d/250_github-credential-helper.sh`:

```bash
#!/bin/bash
# GitHub plugin — install a git credential helper that fetches the PAT
# fresh per git op (so PAT rotation is automatic for git). Runs every boot.
#
# Reads from the standard ra contract:
#   GCP_PROJECT_ID
#   RA_PLUGIN_GITHUB_ENABLED
#   RA_SECRET_GITHUB_PAT_NAME  (gcp secret name, set by buildContainerEnv)
set -euo pipefail

GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_GITHUB_ENABLED:-false}" = "true" ] || exit 0
: "${GCP_PROJECT_ID:?GCP_PROJECT_ID not set}"
: "${RA_SECRET_GITHUB_PAT_NAME:?RA_SECRET_GITHUB_PAT_NAME not set — install github plugin with the pat auth provider}"

# Always rewrite (handles rotation of the secret_name itself).
rm -f "${GIT_HELPER_PATH}"
cat > "${GIT_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated by github plugin. Emits GitHub PAT via git credential protocol.
while IFS= read -r line; do
    [[ -z "\$line" ]] && break
done
pat=\$(gcloud secrets versions access latest \\
    --secret='${RA_SECRET_GITHUB_PAT_NAME}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || exit 0
echo "username=x-access-token"
echo "password=\${pat}"
EOF
chmod 0755 "${GIT_HELPER_PATH}"
git config --system credential.helper "${GIT_HELPER_PATH}"
echo "[github-credential-helper] installed ${GIT_HELPER_PATH} for secret ${RA_SECRET_GITHUB_PAT_NAME}"
```

- [ ] **Step 2: Make executable, verify**

```bash
chmod +x plugins/github/workstation-startup.d/250_github-credential-helper.sh
bash -n plugins/github/workstation-startup.d/250_github-credential-helper.sh && echo OK
```

Expected: `OK` (bash syntax check passes).

- [ ] **Step 3: Commit**

```bash
git add plugins/github/workstation-startup.d/250_github-credential-helper.sh
git commit -m "feat(plugin/github): add 250_github-credential-helper.sh"
```

---

### Task 4: Add `workstation-startup.d/260_github-clone.sh`

This script extracts `_ra_first_boot_git()` from the core entrypoint and adds inline SSH→HTTPS normalization (replacing `NormalizeGithubRepoURL`).

**Files:**
- Create: `plugins/github/workstation-startup.d/260_github-clone.sh`

- [ ] **Step 1: Write the script**

Write `plugins/github/workstation-startup.d/260_github-clone.sh`:

```bash
#!/bin/bash
# GitHub plugin — first-boot git identity setup + repo clone.
#
# Reads from the standard ra contract:
#   GIT_USER_NAME, GIT_USER_EMAIL  (set by core)
#   RA_PLUGIN_GITHUB_ENABLED
#   RA_PLUGIN_GITHUB_REPO_URL      (https://, git@github.com:..., or empty)
#
# Guarded by FIRST_BOOT_MARKER — only runs once per workstation.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"
FIRST_BOOT_MARKER="${USER_HOME}/.ra-github-first-boot-done"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_GITHUB_ENABLED:-false}" = "true" ] || exit 0
[ -e "${FIRST_BOOT_MARKER}" ] && exit 0

# Normalize SSH github URLs to HTTPS — workstation has only HTTPS creds (PAT
# via the credential helper) and no SSH keys / known_hosts, so SSH URLs
# would die with "Host key verification failed".
REPO_URL="${RA_PLUGIN_GITHUB_REPO_URL:-}"
case "${REPO_URL}" in
    git@github.com:*)
        rest="${REPO_URL#git@github.com:}"
        rest="${rest%.git}"
        REPO_URL="https://github.com/${rest}.git"
        echo "[github-clone] normalized SSH URL to ${REPO_URL}"
        ;;
esac

# Git identity (needs to exist regardless of clone — used by any subsequent
# git ops the user runs in the workstation).
if [[ -n "${GIT_USER_NAME:-}" ]]; then
    sudo -u "${USER_NAME}" git config --global user.name "${GIT_USER_NAME}"
fi
if [[ -n "${GIT_USER_EMAIL:-}" ]]; then
    sudo -u "${USER_NAME}" git config --global user.email "${GIT_USER_EMAIL}"
fi

if [[ -n "${REPO_URL}" ]] && [[ ! -d "${WORKSPACE_DIR}/.git" ]]; then
    sudo -u "${USER_NAME}" mkdir -p "${WORKSPACE_DIR}"
    sudo -u "${USER_NAME}" git clone "${REPO_URL}" "${WORKSPACE_DIR}" \
        || echo "[github-clone] WARNING: repo clone failed; proceeding anyway."
fi

touch "${FIRST_BOOT_MARKER}"
chown "${USER_NAME}:${USER_NAME}" "${FIRST_BOOT_MARKER}"
```

- [ ] **Step 2: Make executable, verify**

```bash
chmod +x plugins/github/workstation-startup.d/260_github-clone.sh
bash -n plugins/github/workstation-startup.d/260_github-clone.sh && echo OK
```

Expected: `OK`.

- [ ] **Step 3: Verify Phase 1 build still green**

```bash
make build && go test ./...
```

Expected: PASS. Phase 1 is purely additive — nothing in core references the new plugin yet, so all existing tests must still pass unchanged.

- [ ] **Step 4: Commit**

```bash
git add plugins/github/workstation-startup.d/260_github-clone.sh
git commit -m "feat(plugin/github): add 260_github-clone.sh for first-boot identity + clone"
```

---

## Phase 2 — Strip GitHub from core entrypoint + Dockerfile

These touch only embedded shell assets — no Go code, no tests fail. The Go test suite stays green throughout.

### Task 5: Remove the gh CLI install block from the core Dockerfile

**Files:**
- Modify: `internal/scaffold/assets/Dockerfile:18-25`

- [ ] **Step 1: Edit the Dockerfile**

Use Edit to replace this block:

```
# --- GitHub CLI -------------------------------------------------------------
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)
```

with just:

```
RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)
```

- [ ] **Step 2: Verify build still green**

```bash
make build && go test ./...
```

Expected: PASS. The Dockerfile is embedded via `//go:embed`, so `go build` re-embeds the new bytes; no Go test asserts on Dockerfile content.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/assets/Dockerfile
git commit -m "refactor(core): remove gh CLI install — moved to github plugin"
```

---

### Task 6: Strip github_pat dispatch + `_ra_handle_github_pat` from 200_remote-agent-setup.sh

**Files:**
- Modify: `internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh`

- [ ] **Step 1: Remove `_ra_handle_github_pat()` (lines ~61–93)**

Use Edit to remove the entire function block:

```bash
# _ra_handle_github_pat installs a git credential helper that fetches the
# PAT fresh per git op (so rotation is automatic for git), and also exports
# the PAT to /etc/environment + /run/ra/env so `gh` CLI sees it.
# Will move into a github plugin in a follow-up changeset.
_ra_handle_github_pat() {
    local secret_name="$1"
    local target_env_var="$2"

    log "github_pat: installing git credential helper for ${secret_name}"
    cat > "${GIT_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated. Emits GitHub PAT via git credential protocol.
while IFS= read -r line; do
    [[ -z "\$line" ]] && break
done
pat=\$(gcloud secrets versions access latest \\
    --secret='${secret_name}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || exit 0
echo "username=x-access-token"
echo "password=\${pat}"
EOF
    chmod 0755 "${GIT_HELPER_PATH}"
    git config --system credential.helper "${GIT_HELPER_PATH}"
    log "git credential.helper set to ${GIT_HELPER_PATH}."

    local pat_value
    pat_value=$(gcloud secrets versions access latest \
        --secret="${secret_name}" --project="${GCP_PROJECT_ID}" 2>/dev/null || true)
    if [[ -n "${pat_value}" ]]; then
        _ra_export_env "${target_env_var}" "${pat_value}"
        log "Exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."
    fi
}
```

- [ ] **Step 2: Remove the `github_pat)` case from the dispatch loop (~lines 156–159)**

Replace:

```bash
    case "${logical}" in
        github_pat)
            _ra_handle_github_pat "${secret_name}" "${target_env_var}"
            ;;

        *)
            log "generic: fetching ${secret_name} → ${target_env_var}"
            val=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
                2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
            if [[ -n "${val}" ]]; then
                _ra_export_env "${target_env_var}" "${val}"
                log "Exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."
            else
                log "WARNING: ${secret_name} fetch returned empty."
            fi
            ;;
    esac
```

with the unconditional generic body (no `case`):

```bash
    log "fetching ${secret_name} → ${target_env_var}"
    val=$(gcloud secrets versions access latest \
        --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
        2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
    if [[ -n "${val}" ]]; then
        _ra_export_env "${target_env_var}" "${val}"
        log "Exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."
    else
        log "WARNING: ${secret_name} fetch returned empty."
    fi
```

- [ ] **Step 3: Remove the `GIT_HELPER_PATH` constant (~line 32) and the stale-state `rm -f "${GIT_HELPER_PATH}"` (~line 132)**

After Step 2, `GIT_HELPER_PATH` is unused in 200_; the github plugin owns it. Remove:

```bash
GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"
```

and remove the line:

```bash
rm -f "${GIT_HELPER_PATH}"
```

(Leave the `sed -i '/^GH_TOKEN=/d' /etc/environment` for now; the github plugin's `260_github-clone.sh` runs after 200_ and the env file gets repopulated by the plugin's helper. Removing the `sed` is OK too since GH_TOKEN is no longer treated specially — drop it.)

Actually drop the `sed` line as well:

```bash
sed -i '/^GH_TOKEN=/d' /etc/environment
```

- [ ] **Step 4: Update the header comment block (~lines 1–25) to remove the `github_pat` special-case mention**

Replace this section:

```bash
# Logical names with special handling (still conform to the schema):
#     github_pat     → install git credential helper AND export $VAR for `gh`.
#                      (will move into a github plugin in a follow-up changeset)
#     (any other)    → fetch at boot, export $VAR. Rotation requires restart.
```

with:

```bash
# All logical names: fetch at boot, export $VAR. Rotation requires restart.
# Plugins that need per-op rotation (e.g. github) install their own credential
# helpers in workstation-startup.d/ at numbers ≥ 250.
```

- [ ] **Step 5: Verify build green**

```bash
make build && go test ./...
```

Expected: PASS. Same reasoning as Task 5 — embedded asset, no Go test asserts on its content.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh
git commit -m "refactor(core): remove github_pat special-case from entrypoint"
```

---

### Task 7: Remove `_ra_first_boot_git` from 200_remote-agent-setup.sh

**Files:**
- Modify: `internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh`

- [ ] **Step 1: Remove `_ra_first_boot_git` definition (~lines 95–115)**

Use Edit to delete this block in full:

```bash
# _ra_first_boot_git seeds git identity from container env vars and clones
# the configured repo into the workspace dir. Runs once per workstation;
# guarded by FIRST_BOOT_MARKER.
_ra_first_boot_git() {
    log "First boot: configuring git identity and cloning repo."
    : "${GITHUB_REPO_URL:?GITHUB_REPO_URL not set}"
    : "${GIT_USER_NAME:?GIT_USER_NAME not set}"
    : "${GIT_USER_EMAIL:?GIT_USER_EMAIL not set}"

    sudo -u "${USER_NAME}" git config --global user.name "${GIT_USER_NAME}"
    sudo -u "${USER_NAME}" git config --global user.email "${GIT_USER_EMAIL}"

    if [[ ! -d "${WORKSPACE_DIR}/.git" ]]; then
        sudo -u "${USER_NAME}" mkdir -p "${WORKSPACE_DIR}"
        sudo -u "${USER_NAME}" git clone "${GITHUB_REPO_URL}" "${WORKSPACE_DIR}" \
            2> >(while read -r line; do log "git: ${line}"; done) \
            || log "WARNING: repo clone failed; proceeding anyway."
    fi
    touch "${FIRST_BOOT_MARKER}"
    chown "${USER_NAME}:${USER_NAME}" "${FIRST_BOOT_MARKER}"
}
```

- [ ] **Step 2: Remove the call site at the bottom (~lines 176–182)**

Delete:

```bash
# =============================================================================
# First-boot-only: gitconfig + repo clone.
# =============================================================================
FIRST_BOOT_MARKER="${USER_HOME}/.ra-first-boot-done"
if [[ ! -f "${FIRST_BOOT_MARKER}" ]]; then
    _ra_first_boot_git
fi
```

- [ ] **Step 3: Remove now-unused `WORKSPACE_DIR` constant (~line 30)**

```bash
WORKSPACE_DIR="${USER_HOME}/workspace"
```

(Leave `USER_NAME` and `USER_HOME` — they're used elsewhere in the script.)

- [ ] **Step 4: Verify build green**

```bash
make build && go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/assets/workstation-startup.d/200_remote-agent-setup.sh
git commit -m "refactor(core): remove _ra_first_boot_git — moved to github plugin"
```

---

## Phase 3 — Strip `git.github_repo_url` and `secrets.github_pat` from Go core

This is the hard phase: the field appears in the type, the merge, the validator, the templates, the prompts, the create command, and ~5 test files. Each task changes the smallest atomic set that keeps the build green; tests are updated in lock-step with code.

### Task 8: Drop the `defaultGithubPatSecretsBlock` and `GithubRepoURL` from templates

**Files:**
- Modify: `internal/scaffold/templates.go`
- Modify: `internal/scaffold/templates_test.go`

- [ ] **Step 1: Remove `defaultGithubPatSecretsBlock` constant (lines 29–35)**

Edit `internal/scaffold/templates.go` to delete:

```go
// defaultGithubPatSecretsBlock is the hardcoded github_pat secret entry
// rendered into the user config. It will move into a github plugin in a
// follow-up changeset; isolating it here marks the seam.
const defaultGithubPatSecretsBlock = `  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN
`
```

- [ ] **Step 2: Remove `GithubRepoURL` field from `UserValues` (line 13) and `ProjectValues` (line 40)**

In `UserValues`:

```go
type UserValues struct {
	GitUserName   string
	GitUserEmail  string
	GcloudProject string
	// (GithubRepoURL deleted)
	...
```

In `ProjectValues`:

```go
type ProjectValues struct {
	ProjectID string
	// (GithubRepoURL deleted)
}
```

- [ ] **Step 3: Remove `placeholderRepoURL` constant (line 59)**

```go
placeholderRepoURL     = "https://github.com/my-org/my-repo.git"
```

- [ ] **Step 4: Update `RenderUserConfig`**

In `RenderUserConfig` (lines 76–143):
- Delete the line `repoURL := orStr(uv.GithubRepoURL, placeholderRepoURL)` (line 80).
- Delete the `github_repo_url: %s` line from the format string (line 103).
- Delete the trailing `secrets:\n%s` block — replace with `secrets: {}` (so the rendered file is still valid YAML and the `secrets:` key still exists for plugin install to merge into).
- Drop `repoURL` from the `fmt.Fprintf` argument list (line 138) and drop `defaultGithubPatSecretsBlock` (line 141).

The new `git:` block becomes:

```yaml
git:
  user_name: %q
  user_email: %q
```

The new `secrets:` block becomes:

```yaml
# secrets — populated by `ra plugin install` for plugins that declare them.
# Each entry has shape {secret_name, env_var}. Logical names matter to
# plugin scripts (e.g. the github plugin reads RA_SECRET_GITHUB_PAT_NAME).
secrets: {}

# env — plain static env vars forwarded to the container (no secret fetch).
env: {}
```

The `fmt.Fprintf` call becomes (drop both `repoURL` and `defaultGithubPatSecretsBlock`):

```go
fmt.Fprintf(&b, `... full template body ...`,
    gitName, gitEmail,
    projectID,
    region, machine, disk, cluster, conf, sa, img, tag, repo, sleep, shutdown)
```

- [ ] **Step 5: Update `RenderProjectConfig`**

In `RenderProjectConfig` (lines 147–184):
- Delete `repoURL := orStr(pv.GithubRepoURL, placeholderRepoURL)` (line 149).
- Delete the entire `git:` block at the top of the template, since the project-level config no longer has a required git field. (Project may still override `git.user_name`/`user_email`, but nothing's required there — leave it commented out.)
- Drop `repoURL` from the `fmt.Fprintf` arg list.

The new project template body's top:

```yaml
# ra project config — committed to source control.
# Precedence: project overrides user. See user config for defaults and
# the full secrets/env schema.

gcloud:
  project: %s

# Uncomment any of the groups below to override user values for this project.
#
# git:
#   user_name: Project-Specific Name
#   user_email: project@example.com
#
# gcloud:
#   workstation:
...
```

The `fmt.Fprintf` becomes:

```go
fmt.Fprintf(&b, `... template body ...`, projectID)
```

- [ ] **Step 6: Update `internal/scaffold/templates_test.go`**

Delete these expectations from the `want` slices in `TestRenderUserConfig_EmitsAllRequiredKeysUncommented`:

```go
`  github_repo_url: https://github.com/my-org/my-repo.git`,
```

Change `secrets:\n  github_pat:\n    secret_name: github-pat\n    env_var: GH_TOKEN` expectations to:

```go
`secrets: {}`,
```

Delete from `TestRenderUserConfig_ZeroValues_HasDefaults`:

```go
`github_repo_url: https://github.com/my-org/my-repo.git`,
`  github_pat:`,
```

(Replace with `secrets: {}` if not already present.)

Delete the entire `TestRenderUserConfig_AlwaysHasGithubPat` function — it's the test directly enforcing the removed hardcoded secret.

In `TestRenderProjectConfig_EmitsRequiredKeysAndCommentedExamples`:
- Delete the `pv.GithubRepoURL` field from the `ProjectValues` literal.
- Delete `git:` and `  github_repo_url: https://example.com/r.git` from the want list.
- Add `# git:` to the want list (verifies the commented-out override example is present).

- [ ] **Step 7: Run tests**

```bash
go test ./internal/scaffold/...
```

Expected: PASS. If any other test in the package still references `GithubRepoURL`, fix it now (search with `grep -n GithubRepoURL internal/scaffold/`).

- [ ] **Step 8: Commit**

```bash
git add internal/scaffold/templates.go internal/scaffold/templates_test.go
git commit -m "refactor(scaffold): drop github_repo_url + github_pat from rendered templates"
```

---

### Task 9: Drop the github_repo_url prompt and SSH-normalization helpers from scaffold

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Modify: `internal/scaffold/validate.go`
- Modify: `internal/scaffold/scaffold_test.go`
- Modify: `internal/scaffold/validate_test.go`

- [ ] **Step 1: Remove the github_repo_url prompt blocks from `InteractiveScaffold`**

In `internal/scaffold/scaffold.go`, delete two blocks:

Block 1 (lines 125–137, in the `if writeProject` branch — keep the `projectID` prompt and the `pv.ProjectID = projectID` line; delete the github_repo_url prompt + normalization + assignment):

```go
		repoURL, err := Prompt(stdin, opts.Stdout, "github_repo_url", probed.repoURL, ValidateGithubRepoURL)
		if err != nil {
			return err
		}
		if normalized := NormalizeGithubRepoURL(repoURL); normalized != repoURL {
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr, "Converting SSH URL to HTTPS: %s → %s\n", repoURL, normalized)
			}
			repoURL = normalized
		}
		pv.GithubRepoURL = repoURL
		uv.GithubRepoURL = repoURL
```

Block 2 (lines 147–158 in the `if writeUser && !writeProject` branch): same code, delete identically.

- [ ] **Step 2: Remove the now-unused `repoURL` field from `probedDefaults` (line 266)**

In `probeDefaults`:

```go
type probedDefaults struct {
	projectID string
	gitName   string
	gitEmail  string
	// repoURL deleted
}

func probeDefaults(p Prober) probedDefaults {
	return probedDefaults{
		projectID: p.GcloudProject(),
		gitName:   p.GitUserName(),
		gitEmail:  p.GitUserEmail(),
		// p.GitOriginURL() call removed
	}
}
```

(Keep `Prober.GitOriginURL()` — leaving the interface method is harmless and the github plugin could use it later. But cut the call from `probeDefaults`.)

- [ ] **Step 3: Delete `sshGithubRe`, `ValidateGithubRepoURL`, `NormalizeGithubRepoURL` from `internal/scaffold/validate.go`**

Replace the entire file content from line 12 to end with just:

```go
// ws/internal/scaffold/validate.go
package scaffold

import (
	"fmt"
	"regexp"
)

var projectIDRe = regexp.MustCompile(`^[a-z][-a-z0-9]{4,28}[a-z0-9]$`)

// ValidateProjectID checks that id matches the GCP project ID regex.
// Valid GCP project IDs are 6-30 chars total, lowercase letters/digits/hyphens,
// must start with a lowercase letter, must not end with a hyphen.
func ValidateProjectID(id string) error {
	if !projectIDRe.MatchString(id) {
		return fmt.Errorf("invalid project_id %q: must be 6-30 chars, lowercase letters/digits/hyphens, starting with a letter", id)
	}
	return nil
}
```

(Drop the unused `strings` import as well.)

- [ ] **Step 4: Update `internal/scaffold/validate_test.go`**

Delete `TestValidateGithubRepoURL` (lines 44–67) and `TestNormalizeGithubRepoURL` (lines 69–92) entirely. Keep `TestValidateProjectID`.

- [ ] **Step 5: Update `internal/scaffold/scaffold_test.go` — remove the github_repo_url stdin line from each interactive test**

For each of these tests, drop the `"https://github.com/a/b.git",` line from the `stdin` slice that gets joined with `\n`:

- `TestInteractiveScaffold_WritesExhaustiveUserConfig` (~line 18)
- `TestInteractiveScaffold_WithProjectAssetsCopiesAssetsToDotRA` (~line 80)
- `TestInteractiveScaffold_ProjectExists_SkipsAndPreserves` (~line 144)
- `TestInteractiveScaffold_ProjectOnlySkipsGlobal` (~line 206)
- `TestInteractiveScaffold_NoCancelsOnProceed` (~line 243)
- `TestInteractiveScaffold_DeclineWrapsErrCancelled` (~line 266)
- `TestInteractiveScaffold_NilStderrDoesNotPanic` (~line 289)
- `TestInteractiveScaffold_GlobalExists_SkipsAndScaffoldsAssets` (~line 317)

Also drop these expectations from each test's `want` lists:

```go
`  github_repo_url: https://github.com/a/b.git`,
`  github_pat:`,
```

Replace `env: {}` (still expected) and add:

```go
`secrets: {}`,
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/scaffold/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go internal/scaffold/validate.go internal/scaffold/validate_test.go
git commit -m "refactor(scaffold): drop github_repo_url prompt + URL helpers"
```

---

### Task 10: Drop `git.github_repo_url` from config types, merge, validate, and tests

**Files:**
- Modify: `internal/config/types.go`
- Modify: `internal/config/merge.go`
- Modify: `internal/config/validate.go`
- Modify: `internal/config/merge_test.go`
- Modify: `internal/config/validate_test.go`

- [ ] **Step 1: Remove `GithubRepoURL` from `GitConfig`**

Edit `internal/config/types.go`:

```go
type GitConfig struct {
	UserName  string `yaml:"user_name"`
	UserEmail string `yaml:"user_email"`
	// GithubRepoURL deleted — moved to plugins.github.repo_url
}
```

- [ ] **Step 2: Remove `GithubRepoURL` from `Merge`**

Edit `internal/config/merge.go` line 25 — change:

```go
Git: GitConfig{
    UserName:      u.Git.UserName,
    UserEmail:     u.Git.UserEmail,
    GithubRepoURL: pick(project.Git.GithubRepoURL, u.Git.GithubRepoURL),
},
```

to:

```go
Git: GitConfig{
    UserName:  u.Git.UserName,
    UserEmail: u.Git.UserEmail,
},
```

- [ ] **Step 3: Drop the github_repo_url required check + GITHUB_REPO_URL reservation from validate.go**

Edit `internal/config/validate.go`:

In `reservedEnvVars` (line 15) delete `"GITHUB_REPO_URL",`:

```go
var reservedEnvVars = []string{
	"GCP_PROJECT_ID",
	"GIT_USER_NAME",
	"GIT_USER_EMAIL",
	"RA_SECRETS",
}
```

In the `required` slice (lines 29–48) delete this entry:

```go
{"git.github_repo_url", cfg.Git.GithubRepoURL == ""},
```

Delete the github_pat requirement block (lines 57–60):

```go
// github_pat required (shared infrastructure, not Claude-specific).
if _, ok := cfg.Secrets["github_pat"]; !ok {
    errs = append(errs, "secrets.github_pat is required")
}
```

- [ ] **Step 4: Update `internal/config/merge_test.go`**

Delete every `GithubRepoURL: "..."` line from `UserConfig` / `ProjectConfig` / `GitConfig` literals in `TestMerge_UserBaselineNoProject`, `TestMerge_ProjectOverridesUser`, `TestMerge_GitRepoURL_UserDefaultProjectOverride`.

Delete checks like:

```go
cfg.Git.GithubRepoURL != "https://example.com/user.git"
cfg.Git.GithubRepoURL != "https://example.com/r.git"
```

from the conditional expressions. Where deleting them leaves the conditional always true, simplify accordingly.

Delete `TestMerge_GitRepoURL_UserDefaultProjectOverride` entirely (the whole function).

In `TestMerge_SecretsMap_UserSeedsProjectOverrides`, leave the `github_pat` references — that test asserts the generic secret-merge logic and `github_pat` is still a valid logical name (just not hardcoded by core). Keep the assertion on `proj-github-pat` (project override of an arbitrary secret).

- [ ] **Step 5: Update `internal/config/validate_test.go`**

In `fullValidCfg()`:
- Delete `GithubRepoURL: "https://example.com/r.git",` from the `GitConfig` literal.
- Delete the `"github_pat"` entry from the `Secrets` map (no longer required by core).

In `TestValidate_MissingRequiredKeys`, delete this case:

```go
{func(c *config.Config) { c.Git.GithubRepoURL = "" }, "git.github_repo_url"},
```

Delete `TestValidate_GithubPatRequired` entirely.

In `TestValidate_EnvVarCollision_BetweenSecrets` and `TestValidate_EnvVarCollision_SecretAndEnv`:
- The current tests rely on `github_pat` already being in the seed config (so adding another secret with `GH_TOKEN` collides). Since `github_pat` is no longer in the seed, rewrite these to seed an arbitrary collision: e.g. add a `claude_apikey` with `EnvVar: "ANTHROPIC_API_KEY"` and a second secret with the same env var.

New `TestValidate_EnvVarCollision_BetweenSecrets`:

```go
func TestValidate_EnvVarCollision_BetweenSecrets(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	c.Secrets["dup"] = config.SecretEntry{SecretName: "dup", EnvVar: "ANTHROPIC_API_KEY"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("want collision error, got %v", err)
	}
}
```

New `TestValidate_EnvVarCollision_SecretAndEnv`:

```go
func TestValidate_EnvVarCollision_SecretAndEnv(t *testing.T) {
	c := fullValidCfg()
	c.Secrets["claude_apikey"] = config.SecretEntry{SecretName: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	c.Env = map[string]string{"ANTHROPIC_API_KEY": "whatever"}
	if err := config.Validate(c, nil); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("want collision error between secret and env, got %v", err)
	}
}
```

In `TestValidate_EnvVarCollision_Reserved` (~line 127), delete `"GITHUB_REPO_URL"` from the reserved list:

```go
for _, reserved := range []string{"GCP_PROJECT_ID", "GIT_USER_NAME", "RA_SECRETS", "RA_SECRET_FOO_NAME"} {
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/config/...
```

Expected: PASS. If a test still references `GithubRepoURL` it'll be a compile error — search and fix:

```bash
grep -rn "GithubRepoURL\|github_repo_url\|github_pat" internal/config/
```

Should print nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/config/types.go internal/config/merge.go internal/config/validate.go internal/config/merge_test.go internal/config/validate_test.go
git commit -m "refactor(config): drop git.github_repo_url field and github_pat hardcoded validation"
```

---

### Task 11: Drop `cfg.Git.GithubRepoURL` from `cmd/create.go` and `cmd/create_test.go`

**Files:**
- Modify: `cmd/create.go`
- Modify: `cmd/create_test.go`
- Modify: `cmd/setup_test.go`

- [ ] **Step 1: Strip GithubRepoURL from `buildContainerEnv`**

Edit `cmd/create.go` line 29 — delete:

```go
add("GITHUB_REPO_URL", cfg.Git.GithubRepoURL)
```

The function then emits only `GCP_PROJECT_ID`, `GIT_USER_NAME`, `GIT_USER_EMAIL` plus plugin/secret/env pairs.

- [ ] **Step 2: Remove the SSH normalization block from `runCreate`**

Edit `cmd/create.go` lines 84–89 — delete:

```go
// The workstation image ships HTTPS-only credentials (PAT via git helper) —
// no SSH keys, no known_hosts. Rewrite SSH URLs so the clone inside the
// container doesn't die with "Host key verification failed".
if normalized := scaffold.NormalizeGithubRepoURL(cfg.Git.GithubRepoURL); normalized != cfg.Git.GithubRepoURL {
    fmt.Printf("\033[1;33mWARN:\033[0m rewriting SSH github_repo_url to HTTPS for container clone: %s → %s\n",
        cfg.Git.GithubRepoURL, normalized)
    cfg.Git.GithubRepoURL = normalized
}
```

(The github plugin's `260_github-clone.sh` does this normalization inline at first boot.)

- [ ] **Step 3: Remove `github_repo_url` from the `,=` validation map**

Edit `cmd/create.go` lines 90–98 — change:

```go
for field, val := range map[string]string{
    "git_user_name":   cfg.Git.UserName,
    "git_user_email":  cfg.Git.UserEmail,
    "github_repo_url": cfg.Git.GithubRepoURL,
} {
```

to:

```go
for field, val := range map[string]string{
    "git_user_name":  cfg.Git.UserName,
    "git_user_email": cfg.Git.UserEmail,
} {
```

- [ ] **Step 4: Update the `createCmd.Long` description**

Edit `cmd/create.go` line 68 — change:

```go
Requires .ra/config.yaml in the project root with git.github_repo_url and gcloud.project set.`,
```

to:

```go
Requires .ra/config.yaml in the project root with gcloud.project set.`,
```

- [ ] **Step 5: Update `TestBuildContainerEnv_IncludesSecretsAndEnv` in `cmd/create_test.go`**

In the `cfg` literal:
- Delete `GithubRepoURL: "https://example.com/r.git",` from `GitConfig`.
- Delete the `"github_pat":` entry from the `Secrets` map. Keep `claude_oauth` and `my_extra`.

In the `must` map:
- Delete `"GITHUB_REPO_URL": "https://example.com/r.git",`.
- Delete `"RA_SECRET_GITHUB_PAT_NAME": "github-pat",` and `"RA_SECRET_GITHUB_PAT_VAR": "GH_TOKEN",`.

In the `wantSet` map:
- Delete `"github_pat": true,`. Leave `claude_oauth` and `my_extra`.

- [ ] **Step 6: Update `cmd/setup_test.go`**

In `TestSetupInteractive_AlwaysScaffoldsAssets` (~line 197) and `TestSetupInteractive_RerunSkipsExistingAssets` (~line 248), delete the `"https://github.com/a/b.git",` line from the `input` strings.

- [ ] **Step 7: Run full test suite**

```bash
make build && go test ./...
```

Expected: PASS. Any remaining compile error means a missed reference — search for it:

```bash
grep -rn "GithubRepoURL\|github_repo_url" cmd/ internal/ 2>/dev/null
```

Should print nothing (or only matches inside `plugins/github/` which is fine).

- [ ] **Step 8: Commit**

```bash
git add cmd/create.go cmd/create_test.go cmd/setup_test.go
git commit -m "refactor(cmd): drop github_repo_url from create + setup tests"
```

---

## Phase 4 — Documentation

### Task 12: Update README.md and CLAUDE.md

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: README.md — update the "Getting started" section**

Edit `README.md`. The `ra setup` description currently lists:
```
1. **GCP project ID** — the project where the workstation cluster lives
2. **GitHub repo URL** — the repo to clone inside the workstation
3. **Git identity** — name and email for commits inside the workstation
```

Change to:

```
1. **GCP project ID** — the project where the workstation cluster lives
2. **Git identity** — name and email for commits inside the workstation
```

In the `ra setup` quickstart (~line 41–47), add a github plugin install step:

```bash
make install                                 # installs ra to ~/.local/bin
ra setup                                     # writes config.yaml + core assets
ra plugin install ./plugins/github --user    # installs gh CLI + PAT credential helper,
                                             #   prompts for repo_url and PAT
ra plugin install ./plugins/claude --user    # install the in-tree claude plugin
ra create                                    # provisions the workstation
```

In the "How auth works at runtime" section (line 121), change:

```
The `github_pat` logical name is special-cased to also install a git credential helper (will move to a github plugin in a follow-up changeset).
```

to:

```
Plugin scripts in `/etc/workstation-startup.d/` (numbered ≥ 250) run after 200_; they fetch their own helpers and gate on `RA_PLUGIN_<NAME>_ENABLED`. The github plugin, for example, installs a per-git-op credential helper that fetches the PAT fresh from Secret Manager.
```

- [ ] **Step 2: CLAUDE.md — update**

Find the "Subcommands → Core Packages" section. The `cmd/setup.go` bullet currently mentions `github_repo_url` in the prompted list. Change:

```
probes gcloud/git defaults, prompts for ra-only config (project_id, repo_url, git identity)
```

to:

```
probes gcloud/git defaults, prompts for ra-only config (project_id, git identity)
```

In the "Zero-Secrets Design" / "Naming" area, find and remove any mention of `github_pat` being a core requirement. Add a brief line under the plugin layout description:

```
The github plugin (plugins/github/) extracts what used to be hardcoded in core: the `gh` CLI install (Dockerfile.d/), the per-git-op PAT credential helper (workstation-startup.d/250_), and first-boot repo clone (workstation-startup.d/260_). A workstation only does GitHub things if the user runs `ra plugin install ./plugins/github`.
```

- [ ] **Step 3: Final verification**

```bash
make build && go test ./...
```

Expected: PASS.

```bash
grep -rn "github_repo_url\|GithubRepoURL\|_ra_first_boot_git\|_ra_handle_github_pat\|defaultGithubPatSecretsBlock" cmd/ internal/ 2>/dev/null
```

Expected: no results outside docs/comments. If any results show up in `internal/scaffold/assets/` or Go files, fix before committing.

```bash
grep -rn "github" plugins/github/ | head -20
```

Expected: many — confirms the plugin actually contains the github-specific code now.

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: reflect github extraction — ra setup no longer prompts for repo_url"
```

---

## Phase 5 — Manual smoke test (optional but recommended)

Plan checks via `go test ./...` cover the unit-test surface; the bash scripts and the interaction with `ra plugin install` need a manual run. The plan can be marked complete on a clean test run, but if the engineer has a GCP project handy, this validates the end-to-end flow.

### Task 13: End-to-end manual smoke test

- [ ] **Step 1: Build a fresh ra**

```bash
make install
which ra && ra --version 2>&1 | head -1
```

- [ ] **Step 2: Clean install in a scratch directory**

```bash
mkdir -p /tmp/ra-smoke && cd /tmp/ra-smoke
ra setup --force <<EOF
my-test-proj
Smoke Test
smoke@example.com
y
EOF
```

Expected: writes `~/.config/ra/config.yaml` and `.ra/config.yaml`. Confirm neither file mentions `github_repo_url` or `github_pat`:

```bash
grep -E 'github_repo_url|github_pat' ~/.config/ra/config.yaml .ra/config.yaml
```

Expected: no matches.

- [ ] **Step 3: Install the github plugin from the in-tree path**

```bash
cd /home/user/workspace
ra plugin install ./plugins/github --user
# Prompted: enabled? → y
# Prompted: repo_url → https://github.com/your/test-repo.git
# Prompted: auth provider → 1 (pat)
# Prompted: secret value (hidden) → paste a real PAT
```

Confirm config now contains the plugin block:

```bash
grep -A3 "  github:" ~/.config/ra/config.yaml
grep -A3 "  github_pat:" ~/.config/ra/config.yaml
```

Expected: both present.

- [ ] **Step 4: Run `ra create` (only if you want a real workstation)**

```bash
cd /tmp/ra-smoke && ra create
```

After it boots, SSH in and confirm:
- `which gh` → returns `/usr/bin/gh`
- `git config --system credential.helper` → `/usr/local/bin/git-credential-gcp-secret`
- `ls ~/workspace/.git` → exists (repo was cloned)
- `gh auth status` → succeeds (GH_TOKEN was exported)

- [ ] **Step 5: Cleanup**

```bash
ra delete && rm -rf /tmp/ra-smoke
```

- [ ] **Step 6: Final commit (if README needed touchup based on smoke findings)**

If anything surprised you during smoke test, fix it now and commit. Otherwise this task has nothing to commit.

---

## Self-review checklist (run before declaring the plan done)

- [ ] **Spec coverage:** every item in the user request ("github setup, install, config, auth into a plugin") has a corresponding task: `setup` → Task 9; `install` of gh CLI → Task 2; `config` (`git.github_repo_url`, `secrets.github_pat`) → Tasks 8, 10; `auth` (PAT credential helper) → Task 3.
- [ ] **No placeholders:** no "TBD" / "implement later" / "similar to Task N" / "add appropriate handling" anywhere in this document.
- [ ] **Type/identifier consistency:** the github plugin's logical secret name is `github_pat` (matches `RA_SECRET_GITHUB_PAT_NAME` token computed by `logicalNameToEnvToken`). The plugin name is `github` everywhere (matches `RA_PLUGIN_GITHUB_*` env vars). The repo_url field is `repo_url` (not `github_repo_url`) on the plugin side, addressed as `RA_PLUGIN_GITHUB_REPO_URL` in container env.
- [ ] **Existing test coverage:** every test that referenced `GithubRepoURL`, `github_pat`, `GH_TOKEN`, `GITHUB_REPO_URL`, or `https://github.com/a/b.git` has an explicit task to update or delete it.
