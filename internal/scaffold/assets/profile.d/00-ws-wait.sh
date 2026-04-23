#!/bin/bash
# ws: block login-shell startup until entrypoint.sh has finished populating
# runtime secrets. Shipped by the `ws` CLI and baked into the workstation
# image via the Dockerfile COPY of .ws/profile.d.staged/.
#
# Why this exists:
#   sshd (started by the base image's 100-series script) accepts connections
#   before our 200_claude-setup.sh finishes writing /run/ws/env. A login that
#   lands in that window would have no CLAUDE_CODE_OAUTH_TOKEN / GH_TOKEN in
#   the shell environment. This script waits for the entrypoint's sentinel
#   (/run/ws/ready), then sources the env file, before /etc/profile moves on
#   to any user-provided profile.d scripts.
#
# Timeout:
#   60s is generous relative to entrypoint.sh's typical <5s runtime but short
#   enough that a broken/stuck entrypoint doesn't hang SSH logins forever.
#   On timeout we warn and continue with an empty env rather than blocking
#   the shell indefinitely.

WS_SENTINEL="/run/ws/ready"
WS_ENV_FILE="/run/ws/env"
WS_WAIT_TIMEOUT_SECONDS="${WS_WAIT_TIMEOUT_SECONDS:-60}"

if [ ! -e "${WS_SENTINEL}" ]; then
    waited=0
    while [ ! -e "${WS_SENTINEL}" ] && [ "${waited}" -lt "${WS_WAIT_TIMEOUT_SECONDS}" ]; do
        sleep 1
        waited=$((waited + 1))
    done
    if [ ! -e "${WS_SENTINEL}" ]; then
        echo "ws: timed out after ${WS_WAIT_TIMEOUT_SECONDS}s waiting for ${WS_SENTINEL};" \
             "secrets may be unavailable in this shell." >&2
    fi
fi

if [ -r "${WS_ENV_FILE}" ]; then
    set -a
    # shellcheck disable=SC1090
    . "${WS_ENV_FILE}"
    set +a
fi
