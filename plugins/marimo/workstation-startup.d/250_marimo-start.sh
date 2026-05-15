#!/bin/bash
# marimo plugin — start the `marimo edit` notebook server at container boot.
#
# Runs as root from /etc/workstation-startup.d/, after 200_remote-agent-setup.sh
# has sourced secrets/config into /run/ra/env. Drops privileges to the
# workstation user and detaches the server with setsid so it survives the
# startup script's exit.
#
# Idempotency: pgrep on the marimo command line — running the script twice on
# the same boot is a no-op on the second run.
#
# Logging: appends to ~/.marimo.log (no rotation; acceptable for a single-user
# workstation).
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
MARIMO_LOG="${USER_HOME}/.marimo.log"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_MARIMO_ENABLED:-false}" = "true" ] || exit 0

port="${RA_PLUGIN_MARIMO_PORT_FOR_TUNNEL:-2718}"
working_directory="${RA_PLUGIN_MARIMO_WORKING_DIRECTORY:-}"

# Validate port is a positive integer in the legal TCP range. The plugin
# schema declares `int`, but RA_PLUGIN_* values are unquoted env strings and
# nothing on the workstation side re-checks them.
if [[ ! "${port}" =~ ^[0-9]+$ ]] || [ "${port}" -lt 1 ] || [ "${port}" -gt 65535 ]; then
    echo "[marimo-start] invalid port '${port}'; must be 1-65535" >&2
    exit 1
fi

# Resolve working directory: blank → $USER_HOME (matches tmux plugin idiom).
# `~` expands to root's home in a root-run script, so expand it manually
# against $USER_HOME.
if [[ -z "${working_directory}" ]]; then
    working_directory="${USER_HOME}"
else
    working_directory="${working_directory/#\~/${USER_HOME}}"
fi

# Create the directory as the workstation user so it ends up user-owned
# (mkdir as root would leave it root:root and unwritable by the user).
if [[ ! -d "${working_directory}" ]]; then
    if ! sudo -u "${USER_NAME}" mkdir -p "${working_directory}" 2>/dev/null; then
        echo "[marimo-start] warning: could not create '${working_directory}'; falling back to home." >&2
        working_directory="${USER_HOME}"
    fi
fi

# Idempotency: a previous boot (or a previous run of this script) may have
# left marimo running. The `--headless` anchor avoids false matches on
# unrelated commands that happen to contain the word "marimo".
if pgrep -u "${USER_NAME}" -f "marimo edit --headless" >/dev/null 2>&1; then
    echo "[marimo-start] marimo edit already running; skipping."
    exit 0
fi

# Port collision pre-check — without this, marimo binds-fails silently into
# the log and the user sees an unreachable port with no obvious cause.
if command -v ss >/dev/null 2>&1 && ss -tlnH "sport = :${port}" 2>/dev/null | grep -q .; then
    echo "[marimo-start] port ${port} already in use; aborting." >&2
    exit 1
fi

# Launch detached:
#   - setsid --fork puts marimo in its own session/process-group so it survives
#     this script's exit and is immune to any SIGHUP from session teardown.
#   - env HOME=... PATH=... — /etc/workstation-startup.d/ runs with a near-empty
#     environment; without HOME marimo writes config/state under / or /root.
#   - exec marimo replaces bash so there's no extra shell PID in `pgrep`.
#   - </dev/null detaches stdin so the daemon has no controlling tty.
sudo -u "${USER_NAME}" \
    env HOME="${USER_HOME}" PATH="/usr/local/bin:/usr/bin:/bin" \
    setsid --fork bash -c \
    "cd '${working_directory}' && exec marimo edit --headless --host 0.0.0.0 --port ${port} --no-token >> '${MARIMO_LOG}' 2>&1" \
    </dev/null

echo "[marimo-start] started marimo edit on port ${port} in ${working_directory}"
