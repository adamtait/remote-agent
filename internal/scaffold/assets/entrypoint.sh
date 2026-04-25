#!/bin/bash
#
# Runs at every workstation boot (as root, inside the container).
#
# Contract (v4):
#   ra create emits these env vars into the container config:
#     GCP_PROJECT_ID, GITHUB_REPO_URL, GIT_USER_NAME, GIT_USER_EMAIL
#     RA_CLAUDE_ENABLED=true|false     — toggles the Claude Code harness
#     RA_SECRETS=<pipe-separated logical names>
#     for each logical <name>:
#       RA_SECRET_<UPPERCASE_NAME>_NAME=<gcp secret name>
#       RA_SECRET_<UPPERCASE_NAME>_VAR=<container env var name>
#       (optional, claude_oauth only) RA_SECRET_CLAUDE_OAUTH_OAUTH_ACCOUNT_SECRET_NAME
#     plus any static key=value entries from config.env.
#
# Logical names with special handling (still conform to the schema):
#     claude_oauth   → (claude.enabled=true) fetch, export $VAR, seed
#                      ~/.claude.json onboarding. Handled by claude-setup.sh.
#     claude_apikey  → (claude.enabled=true) install apiKeyHelper (fetches
#                      every call, ~5min TTL). Handled by claude-setup.sh.
#     github_pat     → install git credential helper AND export $VAR for `gh`.
#     (any other)    → fetch at boot, export $VAR. Rotation requires restart.
#
# Harness plugin contract:
#   When RA_CLAUDE_ENABLED=true, /etc/ra/claude-setup.sh is sourced before the
#   dispatch loop. It defines `ra_claude_handle_secret(logical, secret_name,
#   target_env_var)` which returns 0 if it handled the logical name (and the
#   main loop then `continue`s) or 1 to fall through to the main case block.
#
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"

GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"
RA_SETUP_LOG_PATH="/var/log/ra-setup.log"

RA_RUNTIME_DIR="/run/ra"
RA_ENV_FILE="/run/ra/env"
RA_READY_SENTINEL="/run/ra/ready"

log() {
    local msg="[ra-setup] $(date -u +%H:%M:%S) $*"
    echo "${msg}"
    echo "${msg}" >> "${RA_SETUP_LOG_PATH}" 2>/dev/null || true
}

: > "${RA_SETUP_LOG_PATH}" 2>/dev/null || true
chmod 0644 "${RA_SETUP_LOG_PATH}" 2>/dev/null || true

# =============================================================================
# Required env vars (set by ra create)
# =============================================================================
: "${GCP_PROJECT_ID:?GCP_PROJECT_ID not set}"
: "${RA_SECRETS:?RA_SECRETS not set — check workstations config}"

command -v gcloud >/dev/null 2>&1 || {
    log "ERROR: gcloud CLI missing from image; cannot fetch credentials."
    exit 1
}

# Clear stale auth state common to every boot (git helper, GH_TOKEN line).
# Harness-owned state (e.g. Claude's apiKeyHelper, CLAUDE_CODE_OAUTH_TOKEN)
# is cleared by the harness's own setup.sh at source time, so disabling the
# harness doesn't leave ambient credentials behind.
rm -f "${GIT_HELPER_PATH}"
sed -i '/^GH_TOKEN=/d' /etc/environment

# =============================================================================
# Load optional harness plugins.
# Each plugin may define ra_<harness>_handle_secret(logical, secret, env_var)
# which returns 0 if it handled the secret, 1 otherwise. Plugins are sourced
# before the dispatch loop so handler functions are in scope.
# =============================================================================
if [[ "${RA_CLAUDE_ENABLED:-false}" == "true" && -f /etc/ra/claude-setup.sh ]]; then
    log "Claude harness enabled; sourcing /etc/ra/claude-setup.sh."
    # shellcheck source=/dev/null
    source /etc/ra/claude-setup.sh
fi

mkdir -p "${RA_RUNTIME_DIR}"
chmod 0755 "${RA_RUNTIME_DIR}"
rm -f "${RA_READY_SENTINEL}"

# =============================================================================
# Dispatch loop — iterate RA_SECRETS (pipe-separated) and handle each logical name.
# =============================================================================
IFS='|' read -r -a ra_secret_names <<<"${RA_SECRETS}"
for logical in "${ra_secret_names[@]}"; do
    [[ -z "${logical}" ]] && continue
    token=$(echo "${logical}" | tr '[:lower:]' '[:upper:]')
    name_var="RA_SECRET_${token}_NAME"
    env_var_var="RA_SECRET_${token}_VAR"
    secret_name="${!name_var:-}"
    target_env_var="${!env_var_var:-}"

    if [[ -z "${secret_name}" || -z "${target_env_var}" ]]; then
        log "ERROR: secret ${logical} is missing ${name_var} or ${env_var_var}; skipping."
        continue
    fi

    if declare -f ra_claude_handle_secret >/dev/null 2>&1; then
        if ra_claude_handle_secret "${logical}" "${secret_name}" "${target_env_var}"; then
            continue
        fi
    fi

    case "${logical}" in
        github_pat)
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

            pat_value=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" 2>/dev/null || true)
            if [[ -n "${pat_value}" ]]; then
                echo "${target_env_var}=${pat_value}" >> /etc/environment
                umask 077
                if [[ ! -f "${RA_ENV_FILE}" ]]; then
                    : > "${RA_ENV_FILE}"
                    chmod 0644 "${RA_ENV_FILE}"
                fi
                sed -i "/^${target_env_var}=/d" "${RA_ENV_FILE}"
                printf '%s=%s\n' "${target_env_var}" "${pat_value}" >> "${RA_ENV_FILE}"
                log "Exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."
            fi
            ;;

        claude_oauth|claude_apikey)
            # Reached only when the matching harness is disabled — validation
            # should have already rejected the config, so this is defense in
            # depth. Skip quietly to avoid exporting a Claude token into an
            # image that has no Claude binary to consume it.
            log "WARNING: ${logical} configured but RA_CLAUDE_ENABLED!=true; skipping."
            ;;

        *)
            log "generic: fetching ${secret_name} → ${target_env_var}"
            val=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
                2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
            if [[ -n "${val}" ]]; then
                echo "${target_env_var}=${val}" >> /etc/environment
                log "Exported ${target_env_var}."
            else
                log "WARNING: ${secret_name} fetch returned empty."
            fi
            ;;
    esac
done

# =============================================================================
# First-boot-only: gitconfig + repo clone.
# =============================================================================
FIRST_BOOT_MARKER="${USER_HOME}/.ra-first-boot-done"
if [[ ! -f "${FIRST_BOOT_MARKER}" ]]; then
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
fi

touch "${RA_READY_SENTINEL}"
chmod 0644 "${RA_READY_SENTINEL}"
log "Sentinel ${RA_READY_SENTINEL} created; login shells unblocked."

log "Entrypoint complete."
