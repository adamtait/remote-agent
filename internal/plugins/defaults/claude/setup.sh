#!/bin/bash
#
# Claude Code plugin — runtime setup.
# Sourced by entrypoint.sh after all secrets are fetched.
# Secrets (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY) are already exported
# by the entrypoint dispatch loop when this script runs.
#
[ "${RA_PLUGIN_CLAUDE_ENABLED:-false}" = "true" ] || return 0

APIKEY_HELPER_PATH="/usr/local/bin/claude-apikey-helper.sh"
CLAUDE_SETTINGS_DIR="${USER_HOME}/.claude"
CLAUDE_SETTINGS_PATH="${CLAUDE_SETTINGS_DIR}/settings.json"
CLAUDE_CONFIG_PATH="${USER_HOME}/.claude.json"
CLAUDE_INSTALL_SCRIPT="/etc/ra/plugins/claude/install.sh"
CLAUDE_BIN_PATH="${USER_HOME}/.npm-global/bin/claude"

_ra_claude_install_if_missing() {
    if [[ -x "${CLAUDE_BIN_PATH}" ]]; then return 0; fi
    if [[ ! -x "${CLAUDE_INSTALL_SCRIPT}" ]]; then
        log "claude: install script missing; skipping."
        return 0
    fi
    log "claude: installing @anthropic-ai/claude-code (first boot)..."
    bash "${CLAUDE_INSTALL_SCRIPT}" 2> >(while read -r line; do log "claude-install: ${line}"; done) \
        && log "claude: install complete." \
        || log "claude: install failed."
}

_ra_claude_clear_stale_state() {
    rm -f "${APIKEY_HELPER_PATH}"
    sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
    sed -i '/^ANTHROPIC_API_KEY=/d' /etc/environment

    if [[ -f "${CLAUDE_SETTINGS_PATH}" ]] && command -v jq >/dev/null 2>&1; then
        if jq -e '.apiKeyHelper' "${CLAUDE_SETTINGS_PATH}" >/dev/null 2>&1; then
            local tmp; tmp=$(mktemp)
            jq 'del(.apiKeyHelper)' "${CLAUDE_SETTINGS_PATH}" > "${tmp}" \
                && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
        fi
    fi
}

_ra_claude_seed_onboarding() {
    local token="$1"
    local version
    version=$(sudo -u "${USER_NAME}" "${CLAUDE_BIN_PATH}" --version 2>/dev/null \
        | awk '{print $1}' || true)
    [[ "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || version=""

    local tmp; tmp=$(mktemp)
    if [[ -f "${CLAUDE_CONFIG_PATH}" ]]; then
        jq --arg ver "${version}" '
            .hasCompletedOnboarding = true
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
        ' "${CLAUDE_CONFIG_PATH}" > "${tmp}" \
            || { rm -f "${tmp}"; log "claude: jq merge failed."; return 0; }
    else
        jq -n --arg ver "${version}" '
            {hasCompletedOnboarding: true}
            | (if $ver != "" then .lastOnboardingVersion = $ver else . end)
        ' > "${tmp}" \
            || { rm -f "${tmp}"; log "claude: jq render failed."; return 0; }
    fi
    mv "${tmp}" "${CLAUDE_CONFIG_PATH}"
    chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_CONFIG_PATH}"
    chmod 600 "${CLAUDE_CONFIG_PATH}"
    log "claude: seeded ${CLAUDE_CONFIG_PATH}."

    if sudo -u "${USER_NAME}" env CLAUDE_CODE_OAUTH_TOKEN="${token}" \
            "${CLAUDE_BIN_PATH}" -p "respond with OK" 2>/dev/null | grep -q 'OK'; then
        log "claude: smoke test OK."
    else
        log "claude: smoke test did not return OK."
    fi
}

_ra_claude_install_if_missing
_ra_claude_clear_stale_state

case "${RA_PLUGIN_CLAUDE_AUTH_PROVIDER}" in
    oauth)
        if [[ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
            log "claude: CLAUDE_CODE_OAUTH_TOKEN not set; auth may fail."
        else
            echo "CLAUDE_CODE_OAUTH_TOKEN=${CLAUDE_CODE_OAUTH_TOKEN}" >> /etc/environment
            log "claude: exported CLAUDE_CODE_OAUTH_TOKEN."
            _ra_claude_seed_onboarding "${CLAUDE_CODE_OAUTH_TOKEN}"
        fi
        ;;
    apikey)
        if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
            log "claude: ANTHROPIC_API_KEY not set; auth may fail."
        else
            cat > "${APIKEY_HELPER_PATH}" <<EOF
#!/bin/bash
echo "${ANTHROPIC_API_KEY}"
EOF
            chmod 0755 "${APIKEY_HELPER_PATH}"
            mkdir -p "${CLAUDE_SETTINGS_DIR}"
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_DIR}"
            if [[ ! -f "${CLAUDE_SETTINGS_PATH}" ]]; then
                printf '{"apiKeyHelper": "%s"}\n' "${APIKEY_HELPER_PATH}" > "${CLAUDE_SETTINGS_PATH}"
            elif command -v jq >/dev/null 2>&1; then
                tmp=$(mktemp)
                jq --arg h "${APIKEY_HELPER_PATH}" '.apiKeyHelper = $h' \
                    "${CLAUDE_SETTINGS_PATH}" > "${tmp}" && mv "${tmp}" "${CLAUDE_SETTINGS_PATH}"
            fi
            chown "${USER_NAME}:${USER_NAME}" "${CLAUDE_SETTINGS_PATH}"
            log "claude: configured apikey mode."
        fi
        ;;
    *)
        log "claude: unknown auth provider '${RA_PLUGIN_CLAUDE_AUTH_PROVIDER:-}'."
        ;;
esac
