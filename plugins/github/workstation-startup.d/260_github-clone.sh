#!/bin/bash
# GitHub plugin — first-boot git identity setup + repo clone.
#
# Reads from the standard ra contract:
#   GIT_USER_NAME, GIT_USER_EMAIL  (set by core)
#   RA_PLUGIN_GITHUB_ENABLED
#   RA_PLUGIN_GITHUB_REPO_URL      (https://, git@github.com:..., or empty)
#
# Guarded by FIRST_BOOT_MARKER — only runs once per workstation.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"
FIRST_BOOT_MARKER="${USER_HOME}/.ra-github-first-boot-done"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_GITHUB_ENABLED:-false}" = "true" ] || exit 0
[ -e "${FIRST_BOOT_MARKER}" ] && exit 0

# Normalize SSH github URLs to HTTPS — workstation has only HTTPS creds (PAT
# via the credential helper) and no SSH keys / known_hosts, so SSH URLs
# would die with "Host key verification failed".
REPO_URL="${RA_PLUGIN_GITHUB_REPO_URL:-}"
case "${REPO_URL}" in
    git@github.com:*)
        rest="${REPO_URL#git@github.com:}"
        rest="${rest%.git}"
        REPO_URL="https://github.com/${rest}.git"
        echo "[github-clone] normalized SSH URL to ${REPO_URL}"
        ;;
esac

# Git identity (needs to exist regardless of clone — used by any subsequent
# git ops the user runs in the workstation).
if [[ -n "${GIT_USER_NAME:-}" ]]; then
    sudo -u "${USER_NAME}" git config --global user.name "${GIT_USER_NAME}"
fi
if [[ -n "${GIT_USER_EMAIL:-}" ]]; then
    sudo -u "${USER_NAME}" git config --global user.email "${GIT_USER_EMAIL}"
fi

if [[ -n "${REPO_URL}" ]] && [[ ! -d "${WORKSPACE_DIR}/.git" ]]; then
    sudo -u "${USER_NAME}" mkdir -p "${WORKSPACE_DIR}"
    sudo -u "${USER_NAME}" git clone "${REPO_URL}" "${WORKSPACE_DIR}" \
        || echo "[github-clone] WARNING: repo clone failed; proceeding anyway."
fi

touch "${FIRST_BOOT_MARKER}"
chown "${USER_NAME}:${USER_NAME}" "${FIRST_BOOT_MARKER}"
