#!/bin/bash
#
# Claude Code harness — first-boot installer.
#
# Invoked at runtime by claude-setup.sh on container boot when the claude
# binary is missing under /home/user/.npm-global. Runs as root (entrypoint
# context). The 'user' account is provisioned by the code-oss base image's
# startup hooks at boot, so chown/sudo to 'user' only works at runtime —
# attempting this at docker build time fails with "invalid user" because
# /etc/passwd does not yet contain that account.
#
set -euo pipefail

USER_NAME="user"
NPM_PREFIX="/home/${USER_NAME}/.npm-global"

mkdir -p "${NPM_PREFIX}"
chown "${USER_NAME}:${USER_NAME}" "${NPM_PREFIX}"

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm install -g @anthropic-ai/claude-code

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm cache clean --force
