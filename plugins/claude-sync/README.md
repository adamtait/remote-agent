# `claude-sync` plugin

Syncs `~/.claude/` across cloud workstations belonging to the same
(GCP project, git user email) pair. Backed by a per-user GCS bucket and
a 60-second systemd push loop running on each workstation.

## What gets synced

Allowlist — exactly these paths are pushed every cycle:

- `projects/` — Claude Code session transcripts (UUID-named JSONLs) and
  per-project memory directories.
- `todos/` — UUID-keyed todo state.
- `plans/` — UUID-keyed plan documents.
- `session-env/` — per-session environment captures.
- `history.jsonl` — global command log (last-writer-wins across
  workstations).
- `.claude.json` — global config / onboarding state (last-writer-wins).

Excluded (deliberately — local to each workstation):

- `shell-snapshots/`, `sessions/` (PIDs), `plugins/` (npm cache),
  `cache/`, `backups/`, `statsig/`.

## Architecture

See [`SPEC.md`](./SPEC.md) for the full design and
[`implementation-notes.md`](./implementation-notes.md) for the running
log of decisions and deviations.

```
Workstation A ──┐                                  ┌── Workstation C
                ├── gs://ra-claude-<proj>-<hash>/  ──┤
Workstation B ──┘     workstations/<ws-id>/...    └── Workstation D
                      _manifest/<ws-id>.json
```

- Each workstation pushes only to its own `workstations/<ws-id>/`
  prefix → no concurrent-write contention.
- Boot pull merges every other namespace into `~/.claude/` via
  `cp -an` (newer local files win).
- `_manifest/<ws-id>.json` carries `last_push_at_epoch` so the boot
  pull picks the freshest namespace for last-writer-wins files
  (`history.jsonl`, `.claude.json`).

## Installation

```bash
ra plugin install ./plugins/claude-sync
# Prompted for: enabled (default false → say yes), interval_seconds
# (default 60), bucket_name_override (leave empty unless you have a
# specific GCS bucket name to use).
```

The next `ra create` will:

1. Idempotently create the bucket
   `ra-claude-<gcp-project>-<sha1(git-user-email)[:8]>` (or whatever
   you put in `bucket_name_override`).
2. Bind `roles/storage.objectAdmin` on it to the workstation service
   account.
3. Inject `RA_PLUGIN_CLAUDE_SYNC_BUCKET` into the workstations
   container env so the boot scripts know where to push/pull.

## Configuration

| Field                  | Type   | Default                            | Notes                                                                |
|------------------------|--------|------------------------------------|----------------------------------------------------------------------|
| `enabled`              | bool   | `false`                            | Master switch.                                                       |
| `interval_seconds`     | int    | `60`                               | Push cadence. Below 10 starts to amortize the gcloud cold-start cost poorly. |
| `bucket_name_override` | string | (derive `ra-claude-<proj>-<hash>`) | Must match GCS bucket naming (lowercase, 3–63 chars, no underscores). |

## Operations

- **Logs**: `/var/log/ra-claude-sync.log` on each workstation.
- **Service**: `systemctl status ra-claude-sync.service`.
- **Manual push**: `/usr/local/bin/ra-claude-sync-loop.sh` (just run
  the loop body interactively — Ctrl-C to stop).
- **Manual pull**: re-run `/etc/workstation-startup.d/270_claude-sync-pull.sh`
  as root.
- **Inspect a namespace**:
  `gcloud storage ls gs://<bucket>/workstations/<ws-id>/projects/`.

### Cleaning up stale namespaces from deleted workstations

`ra` does not yet garbage-collect deleted workstations' namespaces (v2
will add a bucket lifecycle rule). Until then:

```bash
gcloud storage rm -r "gs://<bucket>/workstations/<old-ws-id>/"
gcloud storage rm "gs://<bucket>/_manifest/<old-ws-id>.json"
```

## ⚠️ Security: transcripts contain whatever Claude saw

Session JSONLs under `projects/<cwd>/` capture full conversational
history including shell output, tool results, and pasted content. Those
flow verbatim into the GCS bucket.

- The bucket is locked down by IAM: only the workstation SA + project
  owners can read it.
- The bucket uses Google-managed default encryption.
- **Do not enable this plugin on workstations that handle data you
  would not also store in a private GCS bucket.**
- For high-sensitivity environments, wait for v2's CMEK / client-side
  encryption support, or run `ra` without the plugin.

## Dependency on the `claude` plugin

claude-sync syncs files under `~/.claude/`. Those files are created by
the `claude` plugin (or by claude-code itself when installed manually).
If neither is present on a workstation, `claude-sync`'s push cycles are
all no-ops — the pull is also a no-op the first time, but on subsequent
boots it will populate `~/.claude/` with state pushed from other
workstations. Recommended: install both plugins together.

## Caveats

- ≤60s of state can be lost if a workstation is killed between push
  cycles (the next workstation creation picks up from where the last
  push left off).
- `gcloud storage rsync` re-uploads whole files on any byte change. A
  long-running session whose JSONL grows to 50 MB will re-upload all
  50 MB once per minute. v2 plans a delta-append approach.
- Per-workstation namespacing means CLAUDE.md memory edits made on
  workstation A and workstation B in parallel use last-merge-order on
  pull; no conflict UI yet.
