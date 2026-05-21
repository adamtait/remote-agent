# ADR 0002: Per-user GCS bucket + per-workstation namespacing for `claude-sync`

- **Status:** Accepted
- **Date:** 2026-05-21
- **Deciders:** @adamtait

## Context

`ra` provisions Google Cloud Workstations whose `claude` plugin installs
Claude Code. Claude writes session transcripts (`projects/<cwd>/<sid>.jsonl`),
CLAUDE.md memory (`projects/<cwd>/memory/*.md`), a global command log
(`history.jsonl`), and config (`.claude.json`) under `~/.claude/`. None of
that state crosses between workstations today — creating a new workstation
loses prior session context, and deleting a workstation destroys its
state entirely.

We want a way to share `~/.claude/` across workstations belonging to the
same user/project. The user wants:

- Session history visible on every workstation.
- CLAUDE.md memory edits propagate.
- State survives workstation deletion.
- Multiple workstations may run in parallel and write at the same time.

### Constraints

- `ra` shells out to `gcloud` for all GCP interactions (CLAUDE.md). Any
  new backend integration must keep that contract — no GCP Go SDK.
- The plugin must noop cleanly on workstations that haven't installed it
  (zero impact on the existing claude plugin).
- The provisioning pipeline in `cmd/create.go` is one
  `logging.Step("name", n, total, ...)` per discrete GCP operation.
- Per-workstation `--container-env` cannot carry a unique workstation
  ID: that env is set on the workstations *config*, which is shared
  across instances using that config.

## Decision

A `claude-sync` plugin that pushes a filtered allowlist of `~/.claude/`
to a per-user GCS bucket every 60s, and pulls every other workstation's
namespace on boot.

### Storage layout

```
gs://ra-claude-<gcp-project>-<sha1(git-user-email)[:8]>/
├── workstations/
│   ├── <ws-id-A>/projects/...
│   ├── <ws-id-A>/history.jsonl
│   ├── <ws-id-A>/claude.json
│   ├── <ws-id-B>/...
│   └── <ws-id-C>/...
└── _manifest/
    ├── <ws-id-A>.json   { last_push_at_epoch: 1716308400, ... }
    └── <ws-id-B>.json
```

Per-workstation namespacing means concurrent pushes never touch the
same GCS object. Reconciliation happens at pull time:

- **UUID-named files** (session JSONLs, todos, plans, session-env):
  unioned across namespaces. Collisions impossible by construction
  (UUIDs).
- **CLAUDE.md memory files**: may collide. Pulled in arbitrary order
  with `cp -an` (no-clobber, archive) so the newest local copy wins;
  if no local copy exists, last namespace processed wins.
- **`history.jsonl` and `.claude.json`** (global, last-writer-wins):
  the pull picks the single namespace whose
  `_manifest/<ws-id>.json` carries the highest
  `last_push_at_epoch`.

### Workstation ID discovery

`WS_ID` is read at runtime from the GCE metadata server
(`http://metadata.google.internal/computeMetadata/v1/instance/name`)
with `hostname` as a fallback. Not injected via `--container-env`
because that env is shared across instances (see constraint above).

### Bucket name

Derived as `ra-claude-<gcp-project>-<sha1(git-user-email)[:8]>` to
avoid leaking the email into the global GCS namespace. Override via
`plugins.claude-sync.bucket_name_override:` — validated up-front by
`internal/gcs.ValidateBucketName` to catch typos before `ra create`
half-provisions.

### IAM model

The workstation service account gets `roles/storage.objectAdmin` on
the bucket. That's enough to read/write the namespace tree (and the
manifest) while staying scoped to one bucket. Same SA pattern as
secret-accessor + artifactregistry.reader.

### Loop semantics

The systemd unit `ra-claude-sync.service` runs as root, uses
`Restart=always` (with `StartLimitIntervalSec=600`/`StartLimitBurst=20`
to bound restart storms), and pushes every `interval_seconds` (default
60). All gcloud calls log to `/var/log/ra-claude-sync.log`. If
`/run/ra/env` is not present after 5 minutes the loop exits 1 and
systemd retries with a 30-second backoff.

## Alternatives considered

- **Cloud Filestore (NFS)**: real-time, but ~$0.20/GB/mo floor and
  adds VPC/network complexity. Overkill for ~50 MB of state.
- **Persistent disk attached per user**: strong consistency but only
  one workstation can mount RW at a time, defeating the parallel-
  workstations use case.
- **Single shared namespace (no per-workstation prefix)**: simpler
  on read, but two parallel workstations writing the same path race
  and can corrupt session JSONLs. Rejected.
- **Inotify-driven Go daemon (real-time push)**: better latency and
  bandwidth profile, but a meaningfully larger surface area than the
  problem demands at v1. Deferred to v2.
- **Pre-shutdown systemd `ExecStop=` final flush**: closes the ≤60s
  data-loss window but requires confirming Cloud Workstations sends
  SIGTERM to the container with enough lead time. Deferred to v2.
- **CLI command surface (`ra claude sync push|pull|status`)**: nicer
  for ops, but the boot pull + steady-state loop covers the common
  case. Deferred to v2.

## Consequences

- New step in the `ra create` pipeline ("Ensure GCS bucket"). Total
  steps go from 10 to 11 even when the plugin is disabled (a no-op
  placeholder step keeps the visible step counter monotonic).
- Anything Claude transcripts capture is replicated to GCS. The
  bucket has IAM-only access, but operators should treat the data as
  sensitive (see the README's security warning). CMEK / client-side
  encryption deferred to v2.
- Stale namespaces from deleted workstations persist forever in the
  bucket; `ra` does not yet GC them. Manual cleanup documented in the
  plugin README. Bucket-lifecycle-based GC deferred to v2.
