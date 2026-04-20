#!/bin/bash
#
# Runs at every workstation boot (as root, inside the container).
#
# Credential handling (v2.1):
#   - ZERO secrets in the workstation config. All credentials live in Secret
#     Manager; the container fetches them at runtime via the VM's attached
#     service account.
#
#       * Claude apikey mode: install an apiKeyHelper that refetches every
#                             ~5 min (Claude Code TTL).
#       * Claude oauth  mode: fetch token now, export via /etc/profile.d/.
#       * GitHub PAT:        install a git credential.helper that fetches
#                             on every git operation. No caching by us;
#                             rotations are picked up immediately.
#
#   - First-boot-only steps (gitconfig pointers, repo clone) are guarded by a
#     marker file, because /home/user is a persistent disk.
#
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"
MARKER="${USER_HOME}/.claude-workstation-setup-done"

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
OAUTH_PROFILE_PATH="/etc/profile.d/claude-auth.sh"
GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"

log() { echo "[claude-setup] $*"; }

# =============================================================================
# Claude auth — runs EVERY boot so rotations are picked up.
# =============================================================================
: "${CLAUDE_AUTH_MODE:?CLAUDE_AUTH_MODE not set — check workstations config}"
: "${CLAUDE_AUTH_SECRET_NAME:?CLAUDE_AUTH_SECRET_NAME not set}"
: "${GITHUB_PAT_SECRET_NAME:?GITHUB_PAT_SECRET_NAME not set}"
: "${GCP_PROJECT_ID:?GCP_PROJECT_ID not set}"

command -v gcloud >/dev/null 2>&1 || {
    log "ERROR: gcloud CLI missing from image; cannot fetch credentials."
    exit 1
}

# Clear any stale auth state from a previous mode to prevent conflicts
# (e.g. apiKeyHelper set + OAuth token exported would trigger auth conflict).
rm -f "${OAUTH_PROFILE_PATH}"
rm -f "${APIKEY_HELPER_PATH}"

case "${CLAUDE_AUTH_MODE}" in
    apikey)
        log "Installing apiKeyHelper → ${APIKEY_HELPER_PATH}"
        cat > "${APIKEY_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated. Outputs current ANTHROPIC_API_KEY from Secret Manager.
# Claude Code caches the result for ~5 min (CLAUDE_CODE_API_KEY_HELPER_TTL_MS).
exec gcloud secrets versions access latest \\
    --secret='${CLAUDE_AUTH_SECRET_NAME}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null
EOF
        chmod 0755 "${APIKEY_HELPER_PATH}"

        # Sanity check — run the helper once so a broken config fails loud now
        # rather than at first `claude` invocation.
        if ! "${APIKEY_HELPER_PATH}" | grep -q '.'; then
            log "WARNING: apiKeyHelper returned no value. Check SA permissions."
        fi

        # Wire it into Claude Code user settings. Merge into existing JSON
        # with jq if present, else only write if the file doesn't exist.
        mkdir -p "${CLAUDE_SETTINGS_DIR}"
        chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_DIR}"

        if [[ ! -f "${CLAUDE_SETTINGS_PATH}" ]]; then
            cat > "${CLAUDE_SETTINGS_PATH}" <<EOF
{
  "apiKeyHelper": "${APIKEY_HELPER_PATH}"
}
EOF
        elif command -v jq >/dev/null 2>&1; then
            tmp=$(mktemp)
            jq --arg h "${APIKEY_HELPER_PATH}" '.apiKeyHelper = $h' \
                "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
        else
            log "WARNING: settings.json exists and jq missing; leaving it alone."
        fi
        chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
        log "apiKeyHelper installed and wired into settings.json."
        ;;

    oauth)
        log "Fetching CLAUDE_CODE_OAUTH_TOKEN from Secret Manager..."
        TOKEN=$(gcloud secrets versions access latest \
            --secret="${CLAUDE_AUTH_SECRET_NAME}" \
            --project="${GCP_PROJECT_ID}" 2>/dev/null || true)

        if [[ -z "${TOKEN}" ]]; then
            log "WARNING: OAuth token fetch failed. Check SA permissions."
        else
            # /etc/profile.d/ is sourced by login shells, including the
            # code-oss integrated terminal.
            umask 077
            cat > "${OAUTH_PROFILE_PATH}" <<EOF
# Auto-generated at workstation boot. Do not edit.
export CLAUDE_CODE_OAUTH_TOKEN='${TOKEN}'
EOF
            chmod 0644 "${OAUTH_PROFILE_PATH}"
            log "OAuth token exported via ${OAUTH_PROFILE_PATH}."
        fi

        # Remove any stale apiKeyHelper from settings.json to prevent the
        # "Auth conflict" error Claude Code raises when both are set.
        if [[ -f "${CLAUDE_SETTINGS_PATH}" ]] && command -v jq >/dev/null 2>&1; then
            if jq -e '.apiKeyHelper' "${CLAUDE_SETTINGS_PATH}" >/dev/null 2>&1; then
                tmp=$(mktemp)
                jq 'del(.apiKeyHelper)' "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                    && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
                chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
                log "Removed stale apiKeyHelper from settings.json."
            fi
        fi
        ;;

    *)
        log "ERROR: unknown CLAUDE_AUTH_MODE='${CLAUDE_AUTH_MODE}'"
        exit 1
        ;;
esac

# =============================================================================
# Git credential helper — runs EVERY boot. Rotations of the GitHub PAT in
# Secret Manager are picked up on the next git operation (no caching).
#
# Implements the git credential helper protocol:
#   - argv[1] is the operation: get | store | erase
#   - on `get`, reads protocol/host/etc from stdin and writes username +
#     password to stdout when host is github.com. Other operations are no-ops
#     (we never persist; Secret Manager is the source of truth).
# =============================================================================
log "Installing git credential helper → ${GIT_HELPER_PATH}"
cat > "${GIT_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated. Resolves GitHub credentials from Secret Manager.
set -euo pipefail

op="\${1:-}"
[[ "\${op}" == "get" ]] || exit 0   # silently ignore store/erase/unknown

# Parse stdin (key=value lines, blank line terminates)
declare -A attr
while IFS='=' read -r k v; do
    [[ -z "\${k}" ]] && break
    attr["\${k}"]="\${v}"
done

# Only respond for github.com over https
[[ "\${attr[protocol]:-}" == "https"     ]] || exit 0
[[ "\${attr[host]:-}"     == "github.com" ]] || exit 0

token=\$(gcloud secrets versions access latest \\
    --secret='${GITHUB_PAT_SECRET_NAME}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || exit 0

[[ -z "\${token}" ]] && exit 0

printf 'username=oauth2\n'
printf 'password=%s\n' "\${token}"
EOF
chmod 0755 "${GIT_HELPER_PATH}"

# Sanity check: the helper should respond to a probe request.
if ! printf 'protocol=https\nhost=github.com\n\n' \
        | "${GIT_HELPER_PATH}" get | grep -q '^password='; then
    log "WARNING: git credential helper returned no password. Check SA permissions."
fi

# =============================================================================
# First-boot-only setup below this line.
# =============================================================================
if [[ -f "${MARKER}" ]]; then
    log "First-boot setup already done — skipping git config and repo clone."
    exit 0
fi

# ---- Git config (first boot only — persistent disk preserves ~/.gitconfig) -
log "Configuring git to use the credential helper..."
sudo -u "${USER_NAME}" bash <<EOF
set -euo pipefail
# Wipe any prior credential.helper entries (e.g. from an older v2 install
# that wrote ~/.git-credentials). We want exactly one: ours.
git config --global --unset-all credential.helper 2>/dev/null || true
git config --global credential.helper "${GIT_HELPER_PATH}"
git config --global user.name  "${GIT_USER_NAME:-Developer}"
git config --global user.email "${GIT_USER_EMAIL:-developer@example.com}"
git config --global init.defaultBranch main
git config --global pull.rebase false

# Defense in depth: if a stale .git-credentials file exists from a prior
# install, remove it so it can't shadow the helper.
rm -f "${USER_HOME}/.git-credentials"
EOF

# ---- Clone repo -----------------------------------------------------------
if [[ -n "${GITHUB_REPO_URL:-}" ]]; then
    if [[ -d "${WORKSPACE_DIR}/.git" ]]; then
        log "Workspace already a git repo — skipping clone."
    else
        log "Cloning ${GITHUB_REPO_URL} into ${WORKSPACE_DIR}..."
        sudo -u "${USER_NAME}" git clone "${GITHUB_REPO_URL}" "${WORKSPACE_DIR}" \
            || log "WARNING: clone failed — check PAT scopes and repo URL."
    fi
else
    log "GITHUB_REPO_URL not set — skipping clone."
fi

# ---- Mark done ------------------------------------------------------------
touch "${MARKER}"
chown "${USER_NAME}:${USER_NAME}" "${MARKER}"
log "First-boot setup complete."
