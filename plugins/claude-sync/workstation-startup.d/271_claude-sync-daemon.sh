#!/bin/bash
# claude-sync plugin — install + start the push loop.
#
# Materializes /usr/local/bin/ra-claude-sync-loop.sh and starts it.
# Tries the systemd path first (writes /etc/systemd/system/ra-claude-sync.service
# and enables/starts it). When systemd is not PID 1, falls back to a setsid-
# detached background process (mirroring the marimo plugin's daemon pattern).
#
# Why the fallback exists: Cloud Workstations' default base image runs
# `/google/scripts/entrypoint.sh` as PID 1 — NOT systemd — so `systemctl`
# calls succeed at the binary level but fail to register the unit with any
# init system. Before this fallback, the push loop was never starting in
# production: the unit file landed on disk and nothing ever exec'd it.
#
# Idempotent: re-running overwrites the loop script and (in fallback mode)
# kills the previous loop process before relaunching, so config changes
# (e.g. new interval_seconds) take effect on the next boot.
#
# Runs as root so it inherits the workstation service-account auth that
# gcloud picks up from the metadata server, and can read /home/user/.claude/
# regardless of file mode quirks. All loop chatter goes to
# /var/log/ra-claude-sync.log so operators can diagnose sync regressions
# without bouncing the workstation just to read the journal.
set -euo pipefail

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CLAUDE_SYNC_ENABLED:-false}" = "true" ] || exit 0

LOOP_PATH="/usr/local/bin/ra-claude-sync-loop.sh"
UNIT_PATH="/etc/systemd/system/ra-claude-sync.service"
LOG_FILE="/var/log/ra-claude-sync.log"
# Pidfile must agree with the path the loop script writes to. Lives in
# /run so it auto-clears on reboot — any leftover PID would refer to a
# vanished process anyway.
PID_FILE="/run/ra-claude-sync.pid"

cat > "${LOOP_PATH}" <<'LOOP_EOF'
#!/bin/bash
# ra-claude-sync push loop — installed by 271_claude-sync-daemon.sh.
#
# Waits for /run/ra/env (the core 200_*.sh boot script writes it on
# every boot), then pushes a filtered allowlist of /home/user/.claude/
# to gs://${BUCKET}/claude-sync/workstations/${WS_ID}/ every ${INTERVAL}
# seconds. All data lives under the claude-sync/ prefix so other plugins
# can share the same bucket without collisions.
#
# All gcloud chatter is appended to /var/log/ra-claude-sync.log so a
# silent regression in steady state is auditable. Exits with status 1
# on any unrecoverable startup error (env never appears, plugin
# disabled at startup). Under systemd the Restart=always policy retries;
# under the setsid fallback the loop simply dies until the next
# workstation reboot re-runs 271_*.sh — acceptable because the parent
# script (271_*.sh) re-validates the same preconditions before launching
# us, so a fatal startup exit here would just repeat on every retry.
set -uo pipefail

USER_HOME="/home/user"
LOG_FILE="/var/log/ra-claude-sync.log"
PID_FILE="/run/ra-claude-sync.pid"

mkdir -p "$(dirname "${LOG_FILE}")" "$(dirname "${PID_FILE}")"
exec >> "${LOG_FILE}" 2>&1

# Record our PID so 271_*.sh can find and stop us idempotently on the
# next boot. Cleaned up on exit. The pidfile is informational under
# systemd (which tracks the process itself) but is the authoritative
# handle under the setsid fallback path.
echo $$ > "${PID_FILE}"
trap 'rm -f "${PID_FILE}"' EXIT

log() { echo "[claude-sync-loop] $(date -u +%H:%M:%S) $*"; }

# Wait up to 5 minutes for /run/ra/env to be populated. On a fresh
# workstation boot, this systemd unit may start before the core
# workstation-startup.d/200_*.sh has run. After the timeout we exit 1
# so systemd's Restart=always kicks in with backoff instead of leaving
# the service quietly stalled.
ready=0
for _ in $(seq 1 60); do
    if [ -r /run/ra/env ]; then
        ready=1
        break
    fi
    sleep 5
done
if [ "${ready}" -ne 1 ]; then
    log "/run/ra/env not present after 5min; exiting 1 so systemd restarts us"
    exit 1
fi

set -a && . /run/ra/env && set +a

if [ "${RA_PLUGIN_CLAUDE_SYNC_ENABLED:-false}" != "true" ]; then
    log "plugin disabled at startup; exiting 1 so systemd restarts us if env later changes"
    exit 1
fi
if [ -z "${RA_PLUGIN_CLAUDE_SYNC_BUCKET:-}" ]; then
    log "RA_PLUGIN_CLAUDE_SYNC_BUCKET unset at startup; exiting 1"
    exit 1
fi

BUCKET="${RA_PLUGIN_CLAUDE_SYNC_BUCKET}"
INTERVAL="${RA_PLUGIN_CLAUDE_SYNC_INTERVAL_SECONDS:-60}"
case "${INTERVAL}" in *[!0-9]*|"") INTERVAL=60 ;; esac

WS_ID="$(curl -fs -H 'Metadata-Flavor: Google' \
    'http://metadata.google.internal/computeMetadata/v1/instance/name' 2>>"${LOG_FILE}" \
    || true)"
[ -n "${WS_ID}" ] || WS_ID="$(hostname)"

PREFIX="gs://${BUCKET}/claude-sync/workstations/${WS_ID}"
MANIFEST_PATH="gs://${BUCKET}/claude-sync/_manifest/${WS_ID}.json"

log "starting push loop ws-id=${WS_ID} interval=${INTERVAL}s prefix=${PREFIX}"

push_once() {
    for sub in projects todos plans session-env; do
        if [ -d "${USER_HOME}/.claude/${sub}" ]; then
            gcloud storage rsync --recursive \
                "${USER_HOME}/.claude/${sub}/" "${PREFIX}/${sub}/" || true
        fi
    done
    if [ -f "${USER_HOME}/.claude/history.jsonl" ]; then
        gcloud storage cp "${USER_HOME}/.claude/history.jsonl" \
            "${PREFIX}/history.jsonl" || true
    fi
    if [ -f "${USER_HOME}/.claude.json" ]; then
        gcloud storage cp "${USER_HOME}/.claude.json" \
            "${PREFIX}/claude.json" || true
    fi

    # Manifest carries last_push_at so the boot pull script can pick a
    # single namespace for last-writer-wins files.
    local ts iso tmp
    ts=$(date -u +%s)
    iso=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    tmp=$(mktemp)
    printf '{"last_push_at": "%s", "last_push_at_epoch": %s}\n' "${iso}" "${ts}" > "${tmp}"
    gcloud storage cp "${tmp}" "${MANIFEST_PATH}" || true
    rm -f "${tmp}"
}

while true; do
    push_once
    sleep "${INTERVAL}"
done
LOOP_EOF
chmod 0755 "${LOOP_PATH}"

# PID 1 detection. Cloud Workstations' default image runs
# /google/scripts/entrypoint.sh as PID 1, so /proc/1/comm reads as
# "entrypoint.sh", not "systemd". Note: `systemctl` may still be on
# PATH (Dockerfile.d's soft-assert doesn't catch this) but without
# systemd-as-PID-1 the daemon-reload / enable calls are no-ops.
have_systemd_pid1() {
    [ "$(cat /proc/1/comm 2>/dev/null)" = "systemd" ]
}

start_via_systemd() {
    cat > "${UNIT_PATH}" <<'UNIT_EOF'
[Unit]
Description=ra claude-sync push loop (sync ~/.claude to GCS)
After=network-online.target
Wants=network-online.target
# Restart=always combined with a generous burst window: if startup
# fails (env not ready, plugin disabled), we want systemd to keep
# retrying with backoff rather than parking the unit silently.
StartLimitIntervalSec=600
StartLimitBurst=20

[Service]
Type=simple
ExecStart=/usr/local/bin/ra-claude-sync-loop.sh
Restart=always
RestartSec=30

[Install]
WantedBy=multi-user.target
UNIT_EOF

    systemctl daemon-reload
    # --no-block so we don't wait on the unit's initial cycle.
    systemctl enable --now --no-block ra-claude-sync.service || true
    # `restart` so subsequent `ra create` runs that mutate env take effect.
    systemctl restart ra-claude-sync.service || true
    echo "[claude-sync-daemon] ra-claude-sync.service installed and started (logs: ${LOG_FILE})"
}

start_via_setsid() {
    mkdir -p "$(dirname "${LOG_FILE}")"

    # Idempotency: a re-run of 271_*.sh (or a manual invocation) should
    # not produce two parallel loops. The loop script writes its own PID
    # to PID_FILE on start (cleared on exit). Use that PID directly rather
    # than pgrep-by-pattern, which would also match the calling shell's
    # command line whenever the loop path happens to appear in it (e.g. a
    # shell session that just `cat`ed the script).
    if [ -f "${PID_FILE}" ]; then
        old_pid="$(cat "${PID_FILE}" 2>/dev/null || true)"
        # Validate before kill: empty, non-numeric, or dead-PID values
        # all fall through to the harmless `rm -f` below. Matches the
        # case-pattern style used elsewhere in this file (see INTERVAL
        # validation in the loop body).
        case "${old_pid}" in
            ''|*[!0-9]*)
                ;;
            *)
                if kill -0 "${old_pid}" 2>/dev/null; then
                    echo "[claude-sync-daemon] stopping existing loop pid=${old_pid}"
                    kill "${old_pid}" 2>/dev/null || true
                    # Give the previous loop up to 5s to exit cleanly before SIGKILL.
                    for _ in 1 2 3 4 5; do
                        kill -0 "${old_pid}" 2>/dev/null || break
                        sleep 1
                    done
                    kill -9 "${old_pid}" 2>/dev/null || true
                fi
                ;;
        esac
        rm -f "${PID_FILE}"
    fi

    # setsid --fork detaches the loop into its own session/process-group
    # so it survives this startup script's exit (matches the marimo plugin
    # pattern). The loop script does its own log redirection internally,
    # writes its PID to PID_FILE, and clears it on exit, so we don't need
    # to capture the PID here.
    setsid --fork "${LOOP_PATH}" </dev/null
    echo "[claude-sync-daemon] loop started via setsid (PID 1 is $(cat /proc/1/comm 2>/dev/null || echo unknown); logs: ${LOG_FILE})"
}

if have_systemd_pid1; then
    start_via_systemd
else
    start_via_setsid
fi
