#!/bin/bash
#
# Runs at every workstation boot (as root, inside the container).
# Guarded by a marker file so it only does first-time setup once,
# because /home/user is a persistent disk.
#
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"
MARKER="${USER_HOME}/.claude-workstation-setup-done"

log() { echo "[claude-setup] $*"; }

if [[ -f "${MARKER}" ]]; then
    log "Already configured, skipping."
    exit 0
fi

# ---- Git credentials ------------------------------------------------------
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    log "Configuring git credentials..."
    sudo -u "${USER_NAME}" bash <<EOF
set -euo pipefail
git config --global credential.helper store
umask 077
printf 'https://oauth2:%s@github.com\n' "${GITHUB_TOKEN}" > "${USER_HOME}/.git-credentials"
git config --global user.name  "${GIT_USER_NAME:-Developer}"
git config --global user.email "${GIT_USER_EMAIL:-developer@example.com}"
git config --global init.defaultBranch main
git config --global pull.rebase false
EOF
else
    log "GITHUB_TOKEN not set — skipping git credential setup."
fi

# ---- Clone repo -----------------------------------------------------------
if [[ -n "${GITHUB_REPO_URL:-}" ]]; then
    if [[ -d "${WORKSPACE_DIR}/.git" ]]; then
        log "Workspace already a git repo — skipping clone."
    else
        log "Cloning ${GITHUB_REPO_URL} into ${WORKSPACE_DIR}..."
        sudo -u "${USER_NAME}" git clone "${GITHUB_REPO_URL}" "${WORKSPACE_DIR}" \
            || log "WARNING: clone failed — check PAT scopes and repo URL."
    fi
else
    log "GITHUB_REPO_URL not set — skipping clone."
fi

# ---- Mark done ------------------------------------------------------------
touch "${MARKER}"
chown "${USER_NAME}:${USER_NAME}" "${MARKER}"
log "Setup complete."
