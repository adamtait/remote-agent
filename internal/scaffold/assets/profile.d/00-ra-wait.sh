#!/bin/bash
# ra: block login-shell startup until entrypoint.sh has finished populating
# runtime secrets. Shipped by the `ra` CLI and baked into the workstation
# image via the Dockerfile COPY of .ra/profile.d.staged/.
#
# Why this exists:
#   sshd (started by the base image's 100-series script) accepts connections
#   before our 200_ra-setup.sh finishes writing /run/ra/env. A login that
#   lands in that window would have no plugin-fetched secrets in the shell
#   environment. This script waits for the entrypoint's sentinel
#   (/run/ra/ready), then sources the env file, before /etc/profile moves on
#   to any user-provided profile.d scripts.
#
# Timeout:
#   60s is generous relative to entrypoint.sh's typical <5s runtime but short
#   enough that a broken/stuck entrypoint doesn't hang SSH logins forever.
#   On timeout we warn and continue with an empty env rather than blocking
#   the shell indefinitely.

RA_SENTINEL="/run/ra/ready"
RA_ENV_FILE="/run/ra/env"
RA_WAIT_TIMEOUT_SECONDS="${RA_WAIT_TIMEOUT_SECONDS:-60}"

if [ ! -e "${RA_SENTINEL}" ]; then
    waited=0
    while [ ! -e "${RA_SENTINEL}" ] && [ "${waited}" -lt "${RA_WAIT_TIMEOUT_SECONDS}" ]; do
        sleep 1
        waited=$((waited + 1))
    done
    if [ ! -e "${RA_SENTINEL}" ]; then
        echo "ra: timed out after ${RA_WAIT_TIMEOUT_SECONDS}s waiting for ${RA_SENTINEL};" \
             "secrets may be unavailable in this shell." >&2
    fi
fi

if [ -r "${RA_ENV_FILE}" ]; then
    set -a
    # shellcheck disable=SC1090
    . "${RA_ENV_FILE}"
    set +a
fi
