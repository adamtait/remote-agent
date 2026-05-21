#!/bin/bash
# claude-sync plugin — one-shot boot pull.
#
# Pulls every workstation's namespace under gs://${BUCKET}/workstations/ and
# merges into /home/user/.claude/. Idempotent. Bails fast when the plugin
# is disabled or the bucket env var is missing.
#
# Files that are namespace-scoped and UUID-named (projects/, todos/, plans/,
# session-env/) are pulled additively — collisions across workstations are
# impossible for session JSONLs. CLAUDE.md memory files MAY collide and use
# last-writer-wins-by-merge-order.
#
# Files that are global and last-writer-wins (history.jsonl, .claude.json)
# are pulled from the single namespace whose _manifest/<ws>.json carries the
# newest last_push_at_epoch.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CLAUDE_SYNC_ENABLED:-false}" = "true" ] || exit 0
if [ -z "${RA_PLUGIN_CLAUDE_SYNC_BUCKET:-}" ]; then
    echo "[claude-sync-pull] RA_PLUGIN_CLAUDE_SYNC_BUCKET unset; skipping"
    exit 0
fi

BUCKET="${RA_PLUGIN_CLAUDE_SYNC_BUCKET}"

# Workstation ID via GCE metadata; `hostname` is the fallback for local dev
# or any environment without metadata reachability.
WS_ID="$(curl -fs -H 'Metadata-Flavor: Google' \
    'http://metadata.google.internal/computeMetadata/v1/instance/name' 2>/dev/null \
    || true)"
[ -n "${WS_ID}" ] || WS_ID="$(hostname)"

log() { echo "[claude-sync-pull] $(date -u +%H:%M:%S) $*"; }

sudo -u "${USER_NAME}" mkdir -p "${USER_HOME}/.claude"

# List namespaces. `gcloud storage ls` returns one `gs://bucket/workstations/<ws>/`
# per line; an empty / nonexistent prefix is not an error here.
mapfile -t NAMESPACES < <(gcloud storage ls "gs://${BUCKET}/workstations/" 2>/dev/null | sort -u || true)

if [ "${#NAMESPACES[@]}" -eq 0 ]; then
    log "no namespaces under gs://${BUCKET}/workstations/ — first boot"
    exit 0
fi

# Per-namespace merge of additive directories. Skip our own namespace —
# nothing useful to pull from it (and it may not exist yet).
for ns in "${NAMESPACES[@]}"; do
    ns_id="${ns%/}"; ns_id="${ns_id##*/}"
    [ -z "${ns_id}" ] && continue
    [ "${ns_id}" = "${WS_ID}" ] && continue
    log "merging namespace ${ns_id}"
    for sub in projects todos plans session-env; do
        sudo -u "${USER_NAME}" mkdir -p "${USER_HOME}/.claude/${sub}"
        # --recursive is required for rsync to traverse subdirectories.
        # Suppress errors per-namespace so a single missing subdir doesn't
        # abort the whole pull.
        gcloud storage rsync --recursive "${ns}${sub}/" "${USER_HOME}/.claude/${sub}/" 2>/dev/null || true
    done
done

# Pick the namespace with the newest manifest for last-writer-wins files.
newest_ns=""
newest_ts=0
for ns in "${NAMESPACES[@]}"; do
    ns_id="${ns%/}"; ns_id="${ns_id##*/}"
    [ -z "${ns_id}" ] && continue
    [ "${ns_id}" = "${WS_ID}" ] && continue
    ts="$(gcloud storage cat "gs://${BUCKET}/_manifest/${ns_id}.json" 2>/dev/null \
          | jq -r '.last_push_at_epoch // 0' 2>/dev/null || echo 0)"
    if [ "${ts}" -gt "${newest_ts}" ]; then
        newest_ts="${ts}"
        newest_ns="${ns}"
    fi
done

if [ -n "${newest_ns}" ]; then
    log "pulling last-writer-wins files from ${newest_ns}"
    gcloud storage cp "${newest_ns}history.jsonl" "${USER_HOME}/.claude/history.jsonl" 2>/dev/null || true
    gcloud storage cp "${newest_ns}claude.json"   "${USER_HOME}/.claude.json"          2>/dev/null || true
fi

chown -R "${USER_NAME}:${USER_NAME}" "${USER_HOME}/.claude"
[ -f "${USER_HOME}/.claude.json" ] && chown "${USER_NAME}:${USER_NAME}" "${USER_HOME}/.claude.json"
log "done"
