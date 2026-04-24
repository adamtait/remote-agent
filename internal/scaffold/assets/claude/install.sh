#!/bin/bash
#
# Claude Code harness — build-time installer.
#
# Runs as root during `docker buildx build` when WS_CLAUDE_ENABLED=true.
# Installs the @anthropic-ai/claude-code npm package under the 'user' account
# so the package can self-update at runtime (the base image's code-oss IDE
# runs under that account).
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
