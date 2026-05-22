#!/bin/bash
# claude-sync plugin — one-shot boot pull.
#
# Pulls every workstation's namespace under gs://${BUCKET}/claude-sync/workstations/
# and merges into /home/user/.claude/. Idempotent. Bails fast when the
# plugin is disabled or the bucket env var is missing.
#
# All claude-sync data lives under the claude-sync/ prefix inside the
# bucket so other plugins can share the same bucket without collisions.
#
# Files that are namespace-scoped and UUID-named (projects/, todos/,
# plans/, session-env/) are pulled additively into a tmpdir first, then
# copied into the live tree with `cp -an` so newer local files (e.g.
# unsynced changes from before the workstation last restarted) win over
# the GCS copy. CLAUDE.md memory files MAY collide; last-writer-by-
# merge-order wins.
#
# Files that are global and last-writer-wins across workstations
# (history.jsonl, .claude.json) are pulled from the single namespace
# whose _manifest/<ws>.json carries the newest last_push_at_epoch.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
LOG_FILE="/var/log/ra-claude-sync.log"

# Tee both stdout and stderr through to the shared log file so a sync
# regression leaves an auditable trail beyond the boot journal.
mkdir -p "$(dirname "${LOG_FILE}")"
exec > >(tee -a "${LOG_FILE}") 2>&1

log() { echo "[claude-sync-pull] $(date -u +%H:%M:%S) $*"; }

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_CLAUDE_SYNC_ENABLED:-false}" = "true" ] || exit 0
if [ -z "${RA_PLUGIN_CLAUDE_SYNC_BUCKET:-}" ]; then
    log "RA_PLUGIN_CLAUDE_SYNC_BUCKET unset; skipping"
    exit 0
fi

BUCKET="${RA_PLUGIN_CLAUDE_SYNC_BUCKET}"

# Workstation ID via GCE metadata; `hostname` is the fallback for local
# dev or any environment without metadata reachability.
WS_ID="$(curl -fs -H 'Metadata-Flavor: Google' \
    'http://metadata.google.internal/computeMetadata/v1/instance/name' 2>>"${LOG_FILE}" \
    || true)"
[ -n "${WS_ID}" ] || WS_ID="$(hostname)"
PLUGIN_PREFIX="gs://${BUCKET}/claude-sync"

log "starting pull as ws-id=${WS_ID} from ${PLUGIN_PREFIX}"

mkdir -p "${USER_HOME}/.claude"

# List namespaces. `gcloud storage ls` returns one
# `gs://bucket/claude-sync/workstations/<ws>/` per line; an
# empty/nonexistent prefix is not an error here.
mapfile -t NAMESPACES < <(gcloud storage ls "${PLUGIN_PREFIX}/workstations/" 2>>"${LOG_FILE}" | sort -u || true)

if [ "${#NAMESPACES[@]}" -eq 0 ]; then
    log "no namespaces under ${PLUGIN_PREFIX}/workstations/ — first boot"
    chown -R "${USER_NAME}:${USER_NAME}" "${USER_HOME}/.claude"
    exit 0
fi

# Stage everything into a tmpdir so the existing live tree is unaffected
# until we cp-no-clobber on top.
STAGE="$(mktemp -d)"
trap 'rm -rf "${STAGE}"' EXIT

for ns in "${NAMESPACES[@]}"; do
    ns_id="${ns%/}"; ns_id="${ns_id##*/}"
    [ -z "${ns_id}" ] && continue
    [ "${ns_id}" = "${WS_ID}" ] && continue
    log "staging namespace ${ns_id}"
    for sub in projects todos plans session-env; do
        mkdir -p "${STAGE}/${sub}"
        gcloud storage rsync --recursive \
            "${ns}${sub}/" "${STAGE}/${sub}/" 2>>"${LOG_FILE}" || true
    done
done

# Merge staged content into ~/.claude using `cp -an` (archive + no-
# clobber) so any newer local file wins. Volatile counters / memory
# files keep whichever copy is on disk; first-time fetches still
# populate empty subtrees.
for sub in projects todos plans session-env; do
    [ -d "${STAGE}/${sub}" ] || continue
    mkdir -p "${USER_HOME}/.claude/${sub}"
    cp -an "${STAGE}/${sub}/." "${USER_HOME}/.claude/${sub}/" 2>>"${LOG_FILE}" || true
done

# Pick the namespace with the newest manifest for last-writer-wins files.
newest_ns=""
newest_ts=0
for ns in "${NAMESPACES[@]}"; do
    ns_id="${ns%/}"; ns_id="${ns_id##*/}"
    [ -z "${ns_id}" ] && continue
    [ "${ns_id}" = "${WS_ID}" ] && continue
    # Guard against gcloud cat failure leaving an empty value; jq's
    # `// 0` default only kicks in for valid JSON without the field.
    ts="$(gcloud storage cat "${PLUGIN_PREFIX}/_manifest/${ns_id}.json" 2>>"${LOG_FILE}" \
          | jq -r '.last_push_at_epoch // 0' 2>>"${LOG_FILE}" || echo 0)"
    ts="${ts:-0}"
    case "${ts}" in *[!0-9]*) ts=0 ;; esac
    if [ "${ts}" -gt "${newest_ts}" ]; then
        newest_ts="${ts}"
        newest_ns="${ns}"
    fi
done

if [ -n "${newest_ns}" ]; then
    log "pulling last-writer-wins files from ${newest_ns} (ts=${newest_ts})"
    # For these single files, last-writer-wins is the intent — overwrite
    # local with the freshest namespace copy.
    gcloud storage cp "${newest_ns}history.jsonl" "${USER_HOME}/.claude/history.jsonl" 2>>"${LOG_FILE}" || true
    gcloud storage cp "${newest_ns}claude.json"   "${USER_HOME}/.claude.json"          2>>"${LOG_FILE}" || true
fi

chown -R "${USER_NAME}:${USER_NAME}" "${USER_HOME}/.claude"
[ -f "${USER_HOME}/.claude.json" ] && chown "${USER_NAME}:${USER_NAME}" "${USER_HOME}/.claude.json"
log "done"
