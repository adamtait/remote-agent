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

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
OAUTH_PROFILE_PATH="/etc/profile.d/claude-auth.sh"
GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"

CLAUDE_CONFIG_PATH="${USER_HOME}/.claude.json"

CLAUDE_LOG_PATH="/var/log/claude-setup.log"

log() {
    local msg="[claude-setup] $(date -u +%H:%M:%S) $*"
    echo "${msg}"
    # Append to a dedicated log file inside the container. Workstations'
    # parent entrypoint redirects our stdout, and COS host VMs don't run
    # a conventional syslog daemon — so a file we can SSH in and read
    # is the most reliable debugging surface. Use append so we can see
    # multiple boots if anything weird is happening.
    echo "${msg}" >> "${CLAUDE_LOG_PATH}" 2>/dev/null || true
}

# Pre-seed ~/.claude.json so interactive `claude` skips the browser-based
# onboarding wizard. Headless VMs can't complete that flow even though the
# OAuth token itself works. Requires oauthAccount metadata stored as a JSON
# blob in Secret Manager (operator copies it from a machine where `claude`
# has already completed interactive login).
seed_claude_onboarding() {
    local token="$1"
    local version
    version=$(sudo -u "${USER_NAME}" claude --version 2>/dev/null | awk '{print $1}' || true)
    if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
        log "WARNING: could not parse claude --version ('${version}'); proceeding without lastOnboardingVersion."
        version=""
    fi

    # Optionally enrich with oauthAccount from Secret Manager (for /status display).
    local acct=""
    if [[ -n "${CLAUDE_OAUTH_ACCOUNT_SECRET_NAME:-}" ]]; then
        log "Fetching oauthAccount metadata from Secret Manager..."
        acct=$(gcloud secrets versions access latest \
            --secret="${CLAUDE_OAUTH_ACCOUNT_SECRET_NAME}" \
            --project="${GCP_PROJECT_ID}" 2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
        if [[ -z "${acct}" ]]; then
            log "WARNING: oauthAccount fetch returned empty; skipping oauthAccount field."
            acct=""
        elif ! jq -e '.accountUuid and .emailAddress and .organizationUuid' \
                >/dev/null 2>&1 <<<"${acct}"; then
            log "WARNING: oauthAccount JSON missing required fields; skipping oauthAccount field."
            acct=""
        fi
    fi

    local tmp
    tmp=$(mktemp)
    if [[ -f "${CLAUDE_CONFIG_PATH}" ]]; then
        # Merge: preserve any other keys Claude Code writes (project state,
        # trusted dirs, MCP servers, etc.).
        jq --arg acct "${acct}" --arg ver "${version}" '
            .hasCompletedOnboarding = true
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
            | (if $acct != "" then .oauthAccount = ($acct | fromjson) else . end)
        ' "${CLAUDE_CONFIG_PATH}" > "${tmp}" || { rm -f "${tmp}"; log "ERROR: jq merge failed; leaving ${CLAUDE_CONFIG_PATH} untouched."; return 0; }
    else
        jq -n --arg acct "${acct}" --arg ver "${version}" '
            {hasCompletedOnboarding: true}
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
            | (if $acct != "" then .oauthAccount = ($acct | fromjson) else . end)
        ' > "${tmp}" || { rm -f "${tmp}"; log "ERROR: jq render failed; skipping onboarding seed."; return 0; }
    fi

    if ! mv "${tmp}" "${CLAUDE_CONFIG_PATH}"; then
        rm -f "${tmp}"
        log "ERROR: failed to write ${CLAUDE_CONFIG_PATH}."
        return 0
    fi
    chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_CONFIG_PATH}"
    chmod 600 "${CLAUDE_CONFIG_PATH}"
    log "Seeded ${CLAUDE_CONFIG_PATH} (hasCompletedOnboarding=true${acct:+, oauthAccount set})."

    # Smoke test — sudo resets env, so pass the token explicitly.
    if sudo -u "${USER_NAME}" env CLAUDE_CODE_OAUTH_TOKEN="${token}" \
            claude -p "respond with OK" 2>/dev/null | grep -q 'OK'; then
        log "Smoke test: claude -p returned OK."
    else
        log "WARNING: claude -p smoke test did not return OK; token or account may be stale."
    fi
}

# Ensure the log file exists and is readable by user so they can
# inspect it without sudo. Truncation happens only once per boot.
: > "${CLAUDE_LOG_PATH}" 2>/dev/null || true
chmod 0644 "${CLAUDE_LOG_PATH}" 2>/dev/null || true

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
sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
sed -i '/^GH_TOKEN=/d' /etc/environment

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
        # Capture stderr to the log so failures (missing scope, missing IAM,
        # token unavailable) surface instead of being silently swallowed.
        TOKEN=$(gcloud secrets versions access latest \
            --secret="${CLAUDE_AUTH_SECRET_NAME}" \
            --project="${GCP_PROJECT_ID}" 2> >(while read -r line; do log "gcloud: ${line}"; done) || true)

        if [[ -z "${TOKEN}" ]]; then
            log "WARNING: OAuth token fetch failed. Check SA permissions."
        else
            # /etc/environment is read by PAM and injected into every session
            # (login, non-login interactive, and IDE-spawned terminals alike).
            # This is the primary delivery mechanism for the token.
            sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
            echo "CLAUDE_CODE_OAUTH_TOKEN=${TOKEN}" >> /etc/environment
            log "OAuth token written to /etc/environment."

            # /etc/profile.d/ is sourced by login shells (e.g. SSH).
            # Keep this as a secondary path so SSH sessions also pick up the token.
            umask 077
            cat > "${OAUTH_PROFILE_PATH}" <<EOF
# Auto-generated at workstation boot. Do not edit.
export CLAUDE_CODE_OAUTH_TOKEN='${TOKEN}'
EOF
            chmod 0644 "${OAUTH_PROFILE_PATH}"
            log "OAuth token also exported via ${OAUTH_PROFILE_PATH} (login shells)."
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

        # Bypass the browser-based onboarding wizard so interactive `claude`
        # works on this headless VM. Only attempted when the token fetch
        # succeeded upstream; otherwise there's nothing to verify against.
        if [[ -n "${TOKEN}" ]]; then
            seed_claude_onboarding "${TOKEN}"
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
# gh CLI wrapper — fetches the PAT per-invocation from Secret Manager and
# forwards to the real /usr/bin/gh. PATH precedence (/usr/local/bin before
# /usr/bin) means every `gh` call goes through this wrapper. The PAT never
# persists on disk; rotations are picked up on the next invocation.
#
# Replaces the prior GH_TOKEN-in-/etc/environment approach, which lost a
# boot-order race against code-oss (terminals spawned from the IDE never
# inherited the env var) and exposed the PAT in a 0644 file.
# =============================================================================
GH_WRAPPER_PATH="/usr/local/bin/gh"
log "Installing gh wrapper → ${GH_WRAPPER_PATH}"

# One-time cleanup: remove any GH_TOKEN= line written by a prior install.
sed -i '/^GH_TOKEN=/d' /etc/environment

cat > "${GH_WRAPPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated at workstation boot. Do not edit.
# Fetches the current GitHub PAT from Secret Manager and forwards to the
# real gh CLI. Token never persists; rotations are picked up on the next
# invocation.
GH_TOKEN=\$(gcloud secrets versions access latest \\
    --secret='${GITHUB_PAT_SECRET_NAME}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || true
exec env GH_TOKEN="\${GH_TOKEN}" /usr/bin/gh "\$@"
EOF
chmod 0755 "${GH_WRAPPER_PATH}"

# Sanity check: the wrapper should authenticate against github.com.
if ! "${GH_WRAPPER_PATH}" auth status >/dev/null 2>&1; then
    log "WARNING: gh wrapper failed auth status. Check SA permissions on ${GITHUB_PAT_SECRET_NAME}."
fi

# =============================================================================
# First-boot-ish setup below this line. Each step has its own idempotence
# check — we intentionally do NOT use a single "already ran" marker file,
# because a marker on the persistent home disk survives workstation recreation
# and blocks legitimate reruns (e.g. if a previous clone failed partway).
# =============================================================================

# ---- Git config (persistent disk preserves ~/.gitconfig, so this is a
#      no-op after the first write, but safe to reapply every boot) -----------
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

# ---- Clone repo (idempotent — skipped if workspace is already a git repo) --
if [[ -n "${GITHUB_REPO_URL:-}" ]]; then
    # The container has HTTPS credentials only (PAT via the git credential
    # helper installed above) — no SSH keys, no known_hosts. If the URL is the
    # SSH form `git@github.com:owner/repo(.git)?`, rewrite it to HTTPS so the
    # clone uses the PAT instead of dying with "Host key verification failed".
    if [[ "${GITHUB_REPO_URL}" =~ ^git@github\.com:(.+)$ ]]; then
        path="${BASH_REMATCH[1]%.git}"
        rewritten="https://github.com/${path}.git"
        log "Rewriting SSH github URL to HTTPS: ${GITHUB_REPO_URL} → ${rewritten}"
        GITHUB_REPO_URL="${rewritten}"
    fi

    if [[ -d "${WORKSPACE_DIR}/.git" ]]; then
        log "Workspace already a git repo — skipping clone."
    else
        log "Cloning ${GITHUB_REPO_URL} into ${WORKSPACE_DIR}..."
        if sudo -u "${USER_NAME}" git clone "${GITHUB_REPO_URL}" "${WORKSPACE_DIR}" \
                2> >(while read -r line; do log "git: ${line}"; done); then
            log "Clone succeeded."
        else
            log "ERROR: clone failed — check PAT scopes, repo URL, and git helper."
        fi
    fi
else
    log "GITHUB_REPO_URL not set — skipping clone."
fi

log "Entrypoint complete."
