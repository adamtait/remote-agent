#!/bin/bash
# github plugin: block login-shell startup until 260_github-clone.sh has
# finished. Without this, sshd accepts a connection as soon as the core
# /run/ra/ready sentinel is up (created by 200_remote-agent-setup.sh) — but
# the github clone runs in a separate startup script and may still be in
# progress, leaving ~/workspace with only an empty .git/.
#
# 260_github-clone.sh always touches the marker on exit (including when the
# plugin is disabled), so this wait completes fast in the no-clone case too.
#
# Sources after /etc/profile.d/00-ra-wait.sh thanks to the 01- prefix.

GITHUB_MARKER="${HOME}/.ra-github-first-boot-done"
RA_WAIT_TIMEOUT_SECONDS="${RA_WAIT_TIMEOUT_SECONDS:-60}"

if [ ! -e "${GITHUB_MARKER}" ]; then
    waited=0
    while [ ! -e "${GITHUB_MARKER}" ] && [ "${waited}" -lt "${RA_WAIT_TIMEOUT_SECONDS}" ]; do
        sleep 1
        waited=$((waited + 1))
    done
    if [ ! -e "${GITHUB_MARKER}" ]; then
        echo "github plugin: timed out after ${RA_WAIT_TIMEOUT_SECONDS}s waiting for ${GITHUB_MARKER};" \
             "workspace clone may still be in progress." >&2
    fi
fi
