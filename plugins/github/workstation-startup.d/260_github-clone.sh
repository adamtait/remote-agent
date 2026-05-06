#!/bin/bash
# GitHub plugin — first-boot git identity setup + repo clone.
#
# Reads from the standard ra contract:
#   GIT_USER_NAME, GIT_USER_EMAIL  (set by core)
#   RA_PLUGIN_GITHUB_ENABLED
#   RA_PLUGIN_GITHUB_REPO_URLS     (pipe-separated list of https:// or git@github.com:... URLs)
#
# Guarded by FIRST_BOOT_MARKER — only runs once per workstation.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"
FIRST_BOOT_MARKER="${USER_HOME}/.ra-github-first-boot-done"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ -e "${FIRST_BOOT_MARKER}" ] && exit 0

# Always touch the marker on exit so the per-plugin profile.d barrier
# (01-github-clone-wait.sh) doesn't hang for 60s when the plugin is disabled
# or when this script aborts before the clone runs.
mark_done() {
    touch "${FIRST_BOOT_MARKER}"
    chown "${USER_NAME}:${USER_NAME}" "${FIRST_BOOT_MARKER}"
}
trap mark_done EXIT

[ "${RA_PLUGIN_GITHUB_ENABLED:-false}" = "true" ] || exit 0

# Git identity (used by any subsequent git ops in the workstation).
if [[ -n "${GIT_USER_NAME:-}" ]]; then
    sudo -u "${USER_NAME}" git config --global user.name "${GIT_USER_NAME}"
fi
if [[ -n "${GIT_USER_EMAIL:-}" ]]; then
    sudo -u "${USER_NAME}" git config --global user.email "${GIT_USER_EMAIL}"
fi

IFS='|' read -r -a _ra_repo_urls <<<"${RA_PLUGIN_GITHUB_REPO_URLS:-}"
for REPO_URL in "${_ra_repo_urls[@]}"; do
    [ -z "${REPO_URL}" ] && continue
    # Normalize SSH GitHub URLs to HTTPS — workstation has only HTTPS creds (PAT
    # via the credential helper) and no SSH keys / known_hosts.
    case "${REPO_URL}" in
        git@github.com:*)
            rest="${REPO_URL#git@github.com:}"
            rest="${rest%.git}"
            REPO_URL="https://github.com/${rest}.git"
            echo "[github-clone] normalized SSH URL to ${REPO_URL}"
            ;;
    esac

    # Extract repo name: last path component, strip .git suffix.
    repo_name="${REPO_URL##*/}"
    repo_name="${repo_name%.git}"

    if [[ -z "${repo_name}" ]]; then
        echo "[github-clone] WARNING: could not determine repo name from ${REPO_URL}; skipping." >&2
        continue
    fi

    clone_target="${WORKSPACE_DIR}/${repo_name}"

    if [[ -d "${clone_target}/.git" ]]; then
        echo "[github-clone] ${clone_target} already cloned; skipping."
        continue
    fi

    sudo -u "${USER_NAME}" mkdir -p "${clone_target}"
    sudo -u "${USER_NAME}" git clone "${REPO_URL}" "${clone_target}" \
        || echo "[github-clone] WARNING: clone of ${REPO_URL} failed; proceeding anyway."
done
unset _ra_repo_urls
