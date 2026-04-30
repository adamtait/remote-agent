#!/bin/bash
# Atelier plugin — VS Code extension install.
#
# Runs as part of /etc/workstation-startup.d/, after secrets are written
# by 200_remote-agent-setup.sh. Atelier needs no secrets; sourcing
# /run/ra/env is only done so the RA_PLUGIN_* gate variables are visible.
# `--force` makes the install idempotent across boots.
set -euo pipefail

USER_NAME="user"
EXTENSION_ID="atelier.atelier"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_ATELIER_ENABLED:-false}" = "true" ] || exit 0

# Locate the code-oss CLI. The base image uses the Cloud Workstations
# code-oss build; the binary is typically `code-oss` but fall back to
# `code` / `codeoss` to stay robust to image changes.
CODE_CLI=""
for cmd in code-oss code codeoss; do
    if sudo -u "${USER_NAME}" command -v "$cmd" >/dev/null 2>&1; then
        CODE_CLI="$cmd"
        break
    fi
done

if [ -z "${CODE_CLI}" ]; then
    echo "atelier: no code-oss CLI found in PATH; skipping extension install" >&2
    exit 0
fi

# code-oss uses Open VSX Registry by default, where the extension is
# published as atelier.atelier. --force makes re-runs idempotent.
sudo -u "${USER_NAME}" "${CODE_CLI}" --install-extension "${EXTENSION_ID}" --force
