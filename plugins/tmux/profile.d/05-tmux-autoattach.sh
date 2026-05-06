#!/bin/bash
# tmux plugin — auto-attach login shells to the persistent tmux session.
#
# Sources after 00-ra-wait.sh (secrets env sourced) and after
# 01-github-clone-wait.sh (clone barrier), thanks to the 05- prefix.

[ "${RA_PLUGIN_TMUX_ENABLED:-false}" = "true" ] || return 0

# Require an interactive tty — guards against scp/rsync/non-interactive ssh
# commands that do not have a terminal and would hang trying to attach.
[ -t 1 ] || return 0

# Skip if already inside tmux. TMUX is set by the tmux client process, so
# this prevents nested attach when the user SSHes from a local tmux session.
[ -z "${TMUX:-}" ] || return 0

# Some terminals (Ghostty, Kitty, WezTerm) advertise a TERM whose terminfo
# entry is not in Ubuntu's ncurses packages. tmux refuses to start with
# "missing or unsuitable terminal: <TERM>" and, because we exec tmux below,
# that closes the SSH session entirely. Fall back to xterm-256color when
# the current TERM has no terminfo, so the session always starts.
if [ -z "${TERM:-}" ] || ! infocmp -- "${TERM}" >/dev/null 2>&1; then
    export TERM=xterm-256color
fi

SESSION_NAME="${RA_PLUGIN_TMUX_SESSION_NAME:-main}"
START_DIRECTORY="${RA_PLUGIN_TMUX_START_DIRECTORY:-}"

# -A: attach-or-create. If the startup script's session was lost (e.g. server
# crash) or the user logs in before 250_tmux-start.sh has finished, this
# recreates it rather than erroring. -c is honored only on creation; it is
# ignored when attaching to an existing session, so passing it is safe.
if [ -n "${START_DIRECTORY}" ]; then
    START_DIRECTORY="${START_DIRECTORY/#\~/${HOME}}"
    if [ -d "${START_DIRECTORY}" ]; then
        exec tmux new-session -A -s "${SESSION_NAME}" -c "${START_DIRECTORY}"
    fi
fi

exec tmux new-session -A -s "${SESSION_NAME}"
