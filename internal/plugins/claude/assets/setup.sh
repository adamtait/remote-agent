#!/bin/bash
#
# Claude Code harness — runtime setup.
#
# Sourced by /etc/workstation-startup.d/200_ra-setup.sh (the main entrypoint)
# at boot when RA_CLAUDE_ENABLED=true. Defines `ra_claude_handle_secret`
# which the main dispatch loop calls for each logical secret name; returns 0
# when it handled the secret, 1 otherwise (the main loop then falls through
# to its own cases).
#
# Depends on functions/constants provided by the main entrypoint:
#   log()                — shared logger
#   RA_RUNTIME_DIR       — /run/ra
#   RA_ENV_FILE          — /run/ra/env
#   GCP_PROJECT_ID       — project id for gcloud
#   USER_NAME            — target container user ("user")
#
# Claude-specific state it owns:
#   /usr/local/bin/claude-apikey-helper.sh     — apiKeyHelper script
#   ${USER_HOME}/.claude/settings.json         — apiKeyHelper wiring
#   ${USER_HOME}/.claude.json                  — onboarding seed
#

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"
CLAUDE_CONFIG_PATH="${USER_HOME}/.claude.json"

CLAUDE_INSTALL_SCRIPT="/etc/ra/claude-install.sh"
CLAUDE_BIN_PATH="${USER_HOME}/.npm-global/bin/claude"

# Install Claude Code on first boot. The unprivileged 'user' account isn't
# in /etc/passwd at docker build time (the code-oss base image provisions
# it via runtime startup hooks), so npm-install-as-user has to happen at
# boot rather than at build. Idempotent: subsequent boots find the binary
# and skip.
_ra_claude_install_if_missing() {
    if [[ -x "${CLAUDE_BIN_PATH}" ]]; then
        return 0
    fi
    if [[ ! -x "${CLAUDE_INSTALL_SCRIPT}" ]]; then
        log "claude: install script ${CLAUDE_INSTALL_SCRIPT} missing or not executable; skipping install."
        return 0
    fi
    log "claude: installing @anthropic-ai/claude-code (first boot)..."
    if bash "${CLAUDE_INSTALL_SCRIPT}" 2> >(while read -r line; do log "claude-install: ${line}"; done); then
        log "claude: install complete."
    else
        log "claude: install failed — claude binary will be unavailable until next boot."
    fi
}

# Clear stale Claude auth state left by previous boots.
_ra_claude_clear_stale_state() {
    rm -f "${APIKEY_HELPER_PATH}"
    sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
    sed -i '/^ANTHROPIC_API_KEY=/d' /etc/environment

    if [[ -f "${CLAUDE_SETTINGS_PATH}" ]] && command -v jq >/dev/null 2>&1; then
        if jq -e '.apiKeyHelper' "${CLAUDE_SETTINGS_PATH}" >/dev/null 2>&1; then
            local tmp
            tmp=$(mktemp)
            jq 'del(.apiKeyHelper)' "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
            log "claude: cleared stale apiKeyHelper from settings.json."
        fi
    fi
}

# Seed ~/.claude.json so interactive `claude` skips the onboarding wizard.
_ra_claude_seed_onboarding() {
    local token="$1"
    local oauth_account_secret="$2"

    # Invoke claude by absolute path: `sudo -u user` resets PATH via secure_path
    # and strips /home/user/.npm-global/bin, so bare `claude` would not resolve.
    local version
    version=$(sudo -u "${USER_NAME}" "${CLAUDE_BIN_PATH}" --version 2>/dev/null | awk '{print $1}' || true)
    if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
        log "claude: could not parse claude --version ('${version}'); proceeding without lastOnboardingVersion."
        version=""
    fi

    local acct=""
    if [[ -n "${oauth_account_secret}" ]]; then
        log "claude: fetching oauthAccount metadata from Secret Manager (${oauth_account_secret})..."
        acct=$(gcloud secrets versions access latest \
            --secret="${oauth_account_secret}" \
            --project="${GCP_PROJECT_ID}" 2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
        if [[ -z "${acct}" ]]; then
            log "claude: oauthAccount fetch returned empty; skipping oauthAccount field."
            acct=""
        elif ! jq -e '.accountUuid and .emailAddress and .organizationUuid' \
                >/dev/null 2>&1 <<<"${acct}"; then
            log "claude: oauthAccount JSON missing required fields; skipping oauthAccount field."
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
        ' "${CLAUDE_CONFIG_PATH}" > "${tmp}" || { rm -f "${tmp}"; log "claude: jq merge failed; leaving ${CLAUDE_CONFIG_PATH} untouched."; return 0; }
    else
        jq -n --arg acct "${acct}" --arg ver "${version}" '
            {hasCompletedOnboarding: true}
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
            | (if $acct != "" then .oauthAccount = ($acct | fromjson) else . end)
        ' > "${tmp}" || { rm -f "${tmp}"; log "claude: jq render failed; skipping onboarding seed."; return 0; }
    fi

    if ! mv "${tmp}" "${CLAUDE_CONFIG_PATH}"; then
        rm -f "${tmp}"
        log "claude: failed to write ${CLAUDE_CONFIG_PATH}."
        return 0
    fi
    chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_CONFIG_PATH}"
    chmod 600 "${CLAUDE_CONFIG_PATH}"
    log "claude: seeded ${CLAUDE_CONFIG_PATH} (hasCompletedOnboarding=true${acct:+, oauthAccount set})."

    if sudo -u "${USER_NAME}" env CLAUDE_CODE_OAUTH_TOKEN="${token}" \
            "${CLAUDE_BIN_PATH}" -p "respond with OK" 2>/dev/null | grep -q 'OK'; then
        log "claude: smoke test returned OK."
    else
        log "claude: smoke test did not return OK; token or account may be stale."
    fi
}

# ra_claude_handle_secret is called by the main dispatch loop for every
# logical secret. Returns 0 if it handled the secret (main loop skips its
# own cases), 1 otherwise.
#
# Args: logical_name, secret_name, target_env_var
ra_claude_handle_secret() {
    local logical="$1"
    local secret_name="$2"
    local target_env_var="$3"

    case "${logical}" in
        claude_oauth)
            log "claude_oauth: fetching ${secret_name} → ${target_env_var}"
            local TOKEN
            TOKEN=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
                2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
            if [[ -z "${TOKEN}" ]]; then
                log "claude: ${secret_name} fetch failed."
                return 0
            fi
            echo "${target_env_var}=${TOKEN}" >> /etc/environment
            umask 077
            local tmp
            tmp=$(mktemp "${RA_RUNTIME_DIR}/.env.XXXXXX")
            printf '%s=%s\n' "${target_env_var}" "${TOKEN}" > "${tmp}"
            chmod 0644 "${tmp}"
            mv "${tmp}" "${RA_ENV_FILE}"
            log "claude: exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."

            local token_upper oauth_account_var
            token_upper=$(echo "${logical}" | tr '[:lower:]' '[:upper:]')
            oauth_account_var="RA_SECRET_${token_upper}_OAUTH_ACCOUNT_SECRET_NAME"
            _ra_claude_seed_onboarding "${TOKEN}" "${!oauth_account_var:-}"
            return 0
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
                log "claude: apiKeyHelper returned no value. Check SA permissions."
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
                local tmp
                tmp=$(mktemp)
                jq --arg h "${APIKEY_HELPER_PATH}" '.apiKeyHelper = $h' \
                    "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                    && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            else
                log "claude: settings.json exists and jq missing; leaving it alone."
            fi
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
            log "claude: apiKeyHelper installed; target env var ${target_env_var} is documentation-only."
            return 0
            ;;
    esac

    return 1
}

# Source-time bootstrap, before the dispatch loop runs.
# Order matters: install must complete before any code path that calls
# `claude` (e.g. _ra_claude_seed_onboarding's smoke test).
_ra_claude_install_if_missing
_ra_claude_clear_stale_state
