#!/bin/bash
# claude-sync plugin — install + enable the systemd push loop.
#
# Heredocs the loop script to /usr/local/bin/ra-claude-sync-loop.sh and the
# systemd unit to /etc/systemd/system/ra-claude-sync.service, then enables
# and (re)starts the service. Idempotent: re-running overwrites both files
# and restarts the unit so config changes (e.g. new interval_seconds) take
# effect on the next boot.
#
# The service runs as root so it inherits the workstation service-account
# auth that gcloud picks up from the metadata server, and so it can read
# /home/user/.claude/ regardless of file mode quirks.
set -euo pipefail

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CLAUDE_SYNC_ENABLED:-false}" = "true" ] || exit 0

LOOP_PATH="/usr/local/bin/ra-claude-sync-loop.sh"
UNIT_PATH="/etc/systemd/system/ra-claude-sync.service"

cat > "${LOOP_PATH}" <<'LOOP_EOF'
#!/bin/bash
# ra-claude-sync push loop — installed by 271_claude-sync-daemon.sh.
#
# Sleeps until /run/ra/env is populated (the core 200_*.sh boot script
# writes it on every boot), then pushes a filtered view of /home/user/.claude
# to gs://${BUCKET}/workstations/${WS_ID}/ every ${INTERVAL} seconds.
set -uo pipefail

USER_HOME="/home/user"

# Wait up to 5 minutes for /run/ra/env. On a fresh boot, this systemd unit
# may start before the core workstation-startup.d/200_*.sh has run.
for _ in $(seq 1 60); do
    [ -r /run/ra/env ] && break
    sleep 5
done

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

if [ "${RA_PLUGIN_CLAUDE_SYNC_ENABLED:-false}" != "true" ]; then
    echo "[claude-sync-loop] plugin disabled; exiting cleanly"
    exit 0
fi
if [ -z "${RA_PLUGIN_CLAUDE_SYNC_BUCKET:-}" ]; then
    echo "[claude-sync-loop] RA_PLUGIN_CLAUDE_SYNC_BUCKET unset; exiting cleanly"
    exit 0
fi

BUCKET="${RA_PLUGIN_CLAUDE_SYNC_BUCKET}"
INTERVAL="${RA_PLUGIN_CLAUDE_SYNC_INTERVAL_SECONDS:-60}"
case "${INTERVAL}" in *[!0-9]*|"") INTERVAL=60 ;; esac

WS_ID="$(curl -fs -H 'Metadata-Flavor: Google' \
    'http://metadata.google.internal/computeMetadata/v1/instance/name' 2>/dev/null \
    || true)"
[ -n "${WS_ID}" ] || WS_ID="$(hostname)"

PREFIX="gs://${BUCKET}/workstations/${WS_ID}"
MANIFEST_PATH="gs://${BUCKET}/_manifest/${WS_ID}.json"

push_once() {
    for sub in projects todos plans session-env; do
        if [ -d "${USER_HOME}/.claude/${sub}" ]; then
            gcloud storage rsync --recursive \
                "${USER_HOME}/.claude/${sub}/" "${PREFIX}/${sub}/" 2>/dev/null || true
        fi
    done
    if [ -f "${USER_HOME}/.claude/history.jsonl" ]; then
        gcloud storage cp "${USER_HOME}/.claude/history.jsonl" \
            "${PREFIX}/history.jsonl" 2>/dev/null || true
    fi
    if [ -f "${USER_HOME}/.claude.json" ]; then
        gcloud storage cp "${USER_HOME}/.claude.json" \
            "${PREFIX}/claude.json" 2>/dev/null || true
    fi

    # Manifest carries last_push_at so the boot pull script can pick a
    # single namespace for last-writer-wins files.
    local ts iso tmp
    ts=$(date -u +%s)
    iso=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    tmp=$(mktemp)
    printf '{"last_push_at": "%s", "last_push_at_epoch": %s}\n' "${iso}" "${ts}" > "${tmp}"
    gcloud storage cp "${tmp}" "${MANIFEST_PATH}" 2>/dev/null || true
    rm -f "${tmp}"
}

echo "[claude-sync-loop] starting; pushing every ${INTERVAL}s to ${PREFIX}"
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

[Service]
Type=simple
ExecStart=/usr/local/bin/ra-claude-sync-loop.sh
Restart=on-failure
RestartSec=15

[Install]
WantedBy=multi-user.target
UNIT_EOF

systemctl daemon-reload
# Use --no-block so we don't wait on the unit's initial cycle.
systemctl enable --now --no-block ra-claude-sync.service || true
# `restart` so subsequent ra create runs that mutate env take effect.
systemctl restart ra-claude-sync.service || true
echo "[claude-sync-daemon] ra-claude-sync.service installed and started"
