#!/bin/bash
# claude-sync plugin — install + enable the systemd push loop.
#
# Heredocs the loop script to /usr/local/bin/ra-claude-sync-loop.sh and
# the systemd unit to /etc/systemd/system/ra-claude-sync.service, then
# enables and (re)starts the service. Idempotent: re-running overwrites
# both files and restarts the unit so config changes (e.g. new
# interval_seconds) take effect on the next boot.
#
# The service runs as root so it inherits the workstation service-
# account auth that gcloud picks up from the metadata server, and so it
# can read /home/user/.claude/ regardless of file mode quirks. All of
# the loop's gcloud chatter goes to /var/log/ra-claude-sync.log so
# operators can diagnose sync regressions without bouncing the
# workstation just to read the journal.
set -euo pipefail

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CLAUDE_SYNC_ENABLED:-false}" = "true" ] || exit 0

LOOP_PATH="/usr/local/bin/ra-claude-sync-loop.sh"
UNIT_PATH="/etc/systemd/system/ra-claude-sync.service"

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
# disabled at startup) so the systemd unit's Restart=always policy
# retries instead of leaving the service silently inactive.
set -uo pipefail

USER_HOME="/home/user"
LOG_FILE="/var/log/ra-claude-sync.log"

mkdir -p "$(dirname "${LOG_FILE}")"
exec >> "${LOG_FILE}" 2>&1

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
echo "[claude-sync-daemon] ra-claude-sync.service installed and started (logs: /var/log/ra-claude-sync.log)"
