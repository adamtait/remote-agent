#!/bin/bash
# Gemini plugin — first-boot npm install.
#
# Runs as part of /etc/workstation-startup.d/, after secrets are written
# by 200_remote-agent-setup.sh. Bails fast on subsequent boots via the
# install marker.
set -euo pipefail

USER_NAME="user"
NPM_PREFIX="/home/${USER_NAME}/.npm-global"
INSTALL_MARKER="/home/${USER_NAME}/.ra-gemini-installed"
GEMINI_BIN="${NPM_PREFIX}/bin/gemini"

# Source fetched secrets in case downstream scripts care; not strictly
# needed for the install itself.
[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_GEMINI_ENABLED:-false}" = "true" ] || exit 0
[ -e "${INSTALL_MARKER}" ] && exit 0
[ -x "${GEMINI_BIN}" ] && { touch "${INSTALL_MARKER}"; exit 0; }

mkdir -p "${NPM_PREFIX}"
chown "${USER_NAME}:${USER_NAME}" "${NPM_PREFIX}"

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm install -g @google/gemini-cli

sudo -u "${USER_NAME}" env NPM_CONFIG_PREFIX="${NPM_PREFIX}" \
    npm cache clean --force

touch "${INSTALL_MARKER}"
chown "${USER_NAME}:${USER_NAME}" "${INSTALL_MARKER}"
