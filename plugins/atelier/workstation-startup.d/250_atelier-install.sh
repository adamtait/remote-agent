#!/bin/bash
# Atelier plugin — VS Code extension install.
#
# Runs as part of /etc/workstation-startup.d/, after secrets are written
# by 200_remote-agent-setup.sh. Atelier needs no secrets; sourcing
# /run/ra/env is only done so the RA_PLUGIN_* gate variables are visible.
#
# Assumes the code-oss image has Open VSX as the configured marketplace
# (the Cloud Workstations base image does). A registry mismatch will fail
# the install but is reported non-fatally so it doesn't break boot.
set -euo pipefail

USER_NAME="user"
EXTENSION_ID="atelier.atelier"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_ATELIER_ENABLED:-false}" = "true" ] || exit 0

# Locate the code-oss CLI. The base image uses the Cloud Workstations
# code-oss build; the binary is typically `code-oss` but fall back to
# `code` / `codeoss` to stay robust to image changes. The lookup runs as
# root (sudo strips PATH and `command` is a shell builtin, not a binary);
# code-oss is system-installed so root's PATH finds it.
CODE_CLI=""
for cmd in code-oss code codeoss; do
    if command -v "$cmd" >/dev/null 2>&1; then
        CODE_CLI="$cmd"
        break
    fi
done

if [ -z "${CODE_CLI}" ]; then
    echo "[atelier] no code-oss CLI found in PATH; skipping extension install" >&2
    exit 0
fi

# Skip if already installed — otherwise --install-extension --force
# re-downloads on every boot.
if sudo -u "${USER_NAME}" "${CODE_CLI}" --list-extensions 2>/dev/null \
        | grep -qixF "${EXTENSION_ID}"; then
    echo "[atelier] ${EXTENSION_ID} already installed"
    exit 0
fi

# code-oss uses Open VSX Registry by default, where the extension is
# published as atelier.atelier. Failures are non-fatal (so a transient
# network blip or registry mismatch doesn't break workstation startup).
if sudo -u "${USER_NAME}" "${CODE_CLI}" \
        --install-extension "${EXTENSION_ID}" --force; then
    echo "[atelier] installed ${EXTENSION_ID} via ${CODE_CLI}"
else
    echo "[atelier] failed to install ${EXTENSION_ID}; check Open VSX configuration" >&2
fi
