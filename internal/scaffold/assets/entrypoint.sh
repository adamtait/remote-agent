#!/bin/bash
#
# Runs at every workstation boot (as root, inside the container).
#
# Contract (v3):
#   ws create emits these env vars into the container config:
#     GCP_PROJECT_ID, GITHUB_REPO_URL, GIT_USER_NAME, GIT_USER_EMAIL
#     WS_SECRETS=<pipe-separated logical names>
#     for each logical <name>:
#       WS_SECRET_<UPPERCASE_NAME>_NAME=<gcp secret name>
#       WS_SECRET_<UPPERCASE_NAME>_VAR=<container env var name>
#       (optional, claude_oauth only) WS_SECRET_CLAUDE_OAUTH_OAUTH_ACCOUNT_SECRET_NAME
#     plus any static key=value entries from config.env.
#
# Logical names with special handling (still conform to the schema):
#     claude_oauth   → fetch, export $VAR, seed ~/.claude.json onboarding.
#     claude_apikey  → install apiKeyHelper (fetches every call, ~5min TTL).
#     github_pat     → install git credential helper AND export $VAR for `gh`.
#     (any other)    → fetch at boot, export $VAR. Rotation requires restart.
#
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"
CLAUDE_CONFIG_PATH="${USER_HOME}/.claude.json"
CLAUDE_LOG_PATH="/var/log/claude-setup.log"

WS_RUNTIME_DIR="/run/ws"
WS_ENV_FILE="/run/ws/env"
WS_READY_SENTINEL="/run/ws/ready"

log() {
    local msg="[claude-setup] $(date -u +%H:%M:%S) $*"
    echo "${msg}"
    echo "${msg}" >> "${CLAUDE_LOG_PATH}" 2>/dev/null || true
}

: > "${CLAUDE_LOG_PATH}" 2>/dev/null || true
chmod 0644 "${CLAUDE_LOG_PATH}" 2>/dev/null || true

# =============================================================================
# Required env vars (set by ws create)
# =============================================================================
: "${GCP_PROJECT_ID:?GCP_PROJECT_ID not set}"
: "${WS_SECRETS:?WS_SECRETS not set — check workstations config}"

command -v gcloud >/dev/null 2>&1 || {
    log "ERROR: gcloud CLI missing from image; cannot fetch credentials."
    exit 1
}

# Clear stale auth state from any previous configuration.
rm -f "${APIKEY_HELPER_PATH}"
rm -f "${GIT_HELPER_PATH}"
sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
sed -i '/^GH_TOKEN=/d' /etc/environment
sed -i '/^ANTHROPIC_API_KEY=/d' /etc/environment

# Strip an apiKeyHelper line from settings.json if it's there.
if [[ -f "${CLAUDE_SETTINGS_PATH}" ]] && command -v jq >/dev/null 2>&1; then
    if jq -e '.apiKeyHelper' "${CLAUDE_SETTINGS_PATH}" >/dev/null 2>&1; then
        tmp=$(mktemp)
        jq 'del(.apiKeyHelper)' "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
            && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
        chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
        log "Cleared stale apiKeyHelper from settings.json."
    fi
fi

# =============================================================================
# Seed ~/.claude.json so interactive `claude` skips the onboarding wizard.
# =============================================================================
seed_claude_onboarding() {
    local token="$1"
    local oauth_account_secret="$2"

    local version
    version=$(sudo -u "${USER_NAME}" claude --version 2>/dev/null | awk '{print $1}' || true)
    if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
        log "WARNING: could not parse claude --version ('${version}'); proceeding without lastOnboardingVersion."
        version=""
    fi

    local acct=""
    if [[ -n "${oauth_account_secret}" ]]; then
        log "Fetching oauthAccount metadata from Secret Manager (${oauth_account_secret})..."
        acct=$(gcloud secrets versions access latest \
            --secret="${oauth_account_secret}" \
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

    if sudo -u "${USER_NAME}" env CLAUDE_CODE_OAUTH_TOKEN="${token}" \
            claude -p "respond with OK" 2>/dev/null | grep -q 'OK'; then
        log "Smoke test: claude -p returned OK."
    else
        log "WARNING: claude -p smoke test did not return OK; token or account may be stale."
    fi
}


mkdir -p "${WS_RUNTIME_DIR}"
chmod 0755 "${WS_RUNTIME_DIR}"
rm -f "${WS_READY_SENTINEL}"

# =============================================================================
# Dispatch loop — iterate WS_SECRETS (pipe-separated) and handle each logical name.
# =============================================================================
IFS='|' read -r -a ws_secret_names <<<"${WS_SECRETS}"
for logical in "${ws_secret_names[@]}"; do
    [[ -z "${logical}" ]] && continue
    token=$(echo "${logical}" | tr '[:lower:]' '[:upper:]')
    name_var="WS_SECRET_${token}_NAME"
    env_var_var="WS_SECRET_${token}_VAR"
    secret_name="${!name_var:-}"
    target_env_var="${!env_var_var:-}"

    if [[ -z "${secret_name}" || -z "${target_env_var}" ]]; then
        log "ERROR: secret ${logical} is missing ${name_var} or ${env_var_var}; skipping."
        continue
    fi

    case "${logical}" in
        claude_oauth)
            log "claude_oauth: fetching ${secret_name} → ${target_env_var}"
            TOKEN=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
                2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
            if [[ -z "${TOKEN}" ]]; then
                log "WARNING: ${secret_name} fetch failed."
            else
                echo "${target_env_var}=${TOKEN}" >> /etc/environment
                umask 077
                tmp=$(mktemp "${WS_RUNTIME_DIR}/.env.XXXXXX")
                printf '%s=%s\n' "${target_env_var}" "${TOKEN}" > "${tmp}"
                chmod 0644 "${tmp}"
                mv "${tmp}" "${WS_ENV_FILE}"
                log "Exported ${target_env_var} via /etc/environment and ${WS_ENV_FILE}."

                oauth_account_var="WS_SECRET_${token}_OAUTH_ACCOUNT_SECRET_NAME"
                seed_claude_onboarding "${TOKEN}" "${!oauth_account_var:-}"
            fi
            ;;

        claude_apikey)
            log "claude_apikey: installing apiKeyHelper for ${secret_name}"
            cat > "${APIKEY_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated. Outputs current value of secret '${secret_name}'.
exec gcloud secrets versions access latest \\
    --secret='${secret_name}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null
EOF
            chmod 0755 "${APIKEY_HELPER_PATH}"
            if ! "${APIKEY_HELPER_PATH}" | grep -q '.'; then
                log "WARNING: apiKeyHelper returned no value. Check SA permissions."
            fi

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
            log "apiKeyHelper installed; target env var ${target_env_var} is documentation-only."
            ;;

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
                if [[ ! -f "${WS_ENV_FILE}" ]]; then
                    : > "${WS_ENV_FILE}"
                    chmod 0644 "${WS_ENV_FILE}"
                fi
                sed -i "/^${target_env_var}=/d" "${WS_ENV_FILE}"
                printf '%s=%s\n' "${target_env_var}" "${pat_value}" >> "${WS_ENV_FILE}"
                log "Exported ${target_env_var} via /etc/environment and ${WS_ENV_FILE}."
            fi
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
FIRST_BOOT_MARKER="${USER_HOME}/.ws-first-boot-done"
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

touch "${WS_READY_SENTINEL}"
chmod 0644 "${WS_READY_SENTINEL}"
log "Sentinel ${WS_READY_SENTINEL} created; login shells unblocked."

log "Entrypoint complete."
