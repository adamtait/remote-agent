#!/bin/bash
# Codex plugin — every-boot auth configuration.
#
# Runs from /etc/workstation-startup.d/, after 200_remote-agent-setup.sh
# has fetched secrets and written /run/ra/env, and after
# 250_codex-install.sh has installed the codex binary.
#
# OAuth path: decodes a base64-encoded auth.json blob into ~/.codex/auth.json.
# (We require base64 because /etc/environment and /run/ra/env can't carry
# multi-line values safely; raw JSON would break shell sourcing.)
#
# apikey path: appends OPENAI_API_KEY to /etc/environment so codex picks
# it up on login.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
CODEX_HOME="${USER_HOME}/.codex"
AUTH_FILE="${CODEX_HOME}/auth.json"

# Source fetched secrets (sets CODEX_AUTH_JSON_B64, OPENAI_API_KEY, etc.).
[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CODEX_ENABLED:-false}" = "true" ] || exit 0

log() { echo "[codex-setup] $(date -u +%H:%M:%S) $*"; }

_clear_stale_state() {
    # Drop any prior auth state so switching providers doesn't leave
    # OPENAI_API_KEY set alongside a cached ChatGPT login (see openai/codex
    # issue #20099 — silent fallback to API-key billing).
    sed -i '/^OPENAI_API_KEY=/d'      /etc/environment
    sed -i '/^CODEX_AUTH_JSON_B64=/d' /etc/environment
    rm -f "${AUTH_FILE}"
}

_clear_stale_state

case "${RA_PLUGIN_CODEX_AUTH_PROVIDER:-}" in
    oauth)
        if [[ -z "${CODEX_AUTH_JSON_B64:-}" ]]; then
            log "CODEX_AUTH_JSON_B64 not set; auth may fail"
        else
            mkdir -p "${CODEX_HOME}"
            chmod 0700 "${CODEX_HOME}"
            chown "${USER_NAME}:${USER_NAME}" "${CODEX_HOME}"

            if printf '%s' "${CODEX_AUTH_JSON_B64}" | base64 -d > "${AUTH_FILE}" 2>/dev/null; then
                chown "${USER_NAME}:${USER_NAME}" "${AUTH_FILE}"
                chmod 0600 "${AUTH_FILE}"
                log "wrote ${AUTH_FILE}"
                # base64 may decode garbage successfully; verify the result parses
                # as JSON when a parser is available. Skip silently if not — the
                # decode succeeded and codex itself will surface bad JSON later.
                if command -v python3 >/dev/null 2>&1; then
                    python3 -c 'import json,sys; json.load(open(sys.argv[1]))' \
                        "${AUTH_FILE}" 2>/dev/null \
                        || log "warning: ${AUTH_FILE} is not valid JSON; auth may fail"
                elif command -v jq >/dev/null 2>&1; then
                    jq empty "${AUTH_FILE}" 2>/dev/null \
                        || log "warning: ${AUTH_FILE} is not valid JSON; auth may fail"
                fi
            else
                rm -f "${AUTH_FILE}"
                log "failed to base64-decode CODEX_AUTH_JSON_B64; auth may fail"
            fi
        fi
        ;;
    apikey)
        if [[ -z "${OPENAI_API_KEY:-}" ]]; then
            log "OPENAI_API_KEY not set; auth may fail"
        else
            echo "OPENAI_API_KEY=${OPENAI_API_KEY}" >> /etc/environment
            log "configured apikey mode"
        fi
        ;;
    *)
        log "unknown auth provider '${RA_PLUGIN_CODEX_AUTH_PROVIDER:-}'"
        ;;
esac
