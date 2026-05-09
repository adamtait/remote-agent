#!/bin/bash
# tmux plugin — start a persistent named session at container boot.
#
# Runs as root from /etc/workstation-startup.d/, after
# 200_remote-agent-setup.sh. Creates the named session as the workstation
# user so it is ready before the first SSH login.
#
# Idempotency: uses `tmux has-session` rather than a marker file — the tmux
# server itself is the authoritative source of truth.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_TMUX_ENABLED:-false}" = "true" ] || exit 0

session_name="${RA_PLUGIN_TMUX_SESSION_NAME:-main}"
start_directory="${RA_PLUGIN_TMUX_START_DIRECTORY:-}"

# Reject names containing '.' or ':' — tmux target syntax characters that
# cause has-session / attach-session to misparse the target.
if [[ ! "${session_name}" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "[tmux-start] invalid session_name '${session_name}'; must match [A-Za-z0-9_-]+" >&2
    exit 1
fi

# Redirect stderr: tmux prints "no server running" to stderr when called
# before the server has ever started, which is the expected first-boot state.
if sudo -u "${USER_NAME}" tmux has-session -t "${session_name}" 2>/dev/null; then
    echo "[tmux-start] session '${session_name}' already exists; skipping."
    exit 0
fi

tmux_args=( new-session -d -s "${session_name}" )

# Default to home when start_directory is unset — plugin.yaml says "leave
# blank for home directory" but the init process CWD is / at boot, so we
# must pass -c explicitly rather than inheriting it.
if [[ -z "${start_directory}" ]]; then
    start_directory="${USER_HOME}"
else
    start_directory="${start_directory/#\~/${USER_HOME}}"
    if [[ ! -d "${start_directory}" ]]; then
        echo "[tmux-start] warning: start_directory '${start_directory}' does not exist; using home." >&2
        start_directory="${USER_HOME}"
    fi
fi
tmux_args+=( -c "${start_directory}" )

# Run tmux as the workstation user — tmux sockets are per-UID; a root-owned
# socket would be inaccessible when the user SSHes in.
sudo -u "${USER_NAME}" tmux "${tmux_args[@]}"
echo "[tmux-start] created session '${session_name}'${start_directory:+ in ${start_directory}}."
