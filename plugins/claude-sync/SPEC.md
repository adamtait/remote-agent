# SPEC: Sync `~/.claude` state across cloud workstations

## Context

The `ra` CLI provisions Google Cloud Workstations whose claude plugin installs Claude Code at `/home/user/.npm-global/bin/claude`. When claude runs, it writes local state to `/home/user/.claude/` and `/home/user/.claude.json` — session transcripts under `projects/<cwd>/<sessionId>.jsonl`, the global config in `.claude.json`, a global command log `history.jsonl`, CLAUDE.md memory files inside `projects/<cwd>/memory/`, todos, plans, etc. That state currently lives only on the workstation's boot disk: it survives stop/start but is **destroyed when the workstation is deleted**, and **never crosses between workstations**. Users who create a new workstation lose all prior session context.

We want to share session history, config, and memory across workstations belonging to the same user/project, with these constraints (chosen up-front):

- Often-parallel workstations writing concurrently → need per-workstation namespacing to avoid corruption.
- Sync scope: history + config + memory (`projects/`, `history.jsonl`, `.claude.json`, CLAUDE.md memory, todos, plans).
- Backend: GCS — fits the existing "shell out to gcloud" pattern, no new VPC/NFS infra.

The intended outcome: when a user creates a new workstation, prior session transcripts and CLAUDE.md memory are present; when they work concurrently from two workstations, neither corrupts the other; when they delete a workstation, its history survives in GCS.

---

## Simplest possible version (v1)

A new `claude-sync` plugin (separate from `claude`, but depending on it) that:

1. **On every boot**, after `260_claude-setup.sh`, pulls every workstation's namespace from a per-user GCS bucket and merges them into the local `~/.claude/`.
2. **Continuously**, runs a 60-second background loop (`systemd` unit) that `gcloud storage rsync`'s a filtered view of `~/.claude/` → `gs://<bucket>/workstations/<this-workstation-id>/`.
3. **No shutdown hook** in v1 — the worst-case data loss is ≤60s of state, which is acceptable given session JSONL files are append-only and the next pull picks them up if the workstation restarts.

Per-workstation namespacing in GCS means concurrent writes from different workstations never touch the same object. Merging happens on read.

GCS bucket is provisioned by `ra create` (idempotent, like the Artifact Registry repo) when `claude-sync` is installed, and the workstation service account gets `roles/storage.objectAdmin` scoped to that bucket.

---

## Architecture diagram

```
 ┌──────────────────────────── Workstation A ────────────────────────────┐
 │                                                                       │
 │   /home/user/.claude/   (boot disk, ephemeral on workstation delete)  │
 │   ├── projects/<cwd>/<sessionId>.jsonl    ← claude writes session log │
 │   ├── projects/<cwd>/memory/*.md          ← CLAUDE.md memory          │
 │   ├── history.jsonl                       ← global command log        │
 │   └── .claude.json                        ← config + onboarding state │
 │             ▲                  │                                      │
 │   pull  (boot, once)      push (every 60s)                            │
 │             │                  ▼                                      │
 │   ┌───────────────────┐  ┌────────────────────────┐                   │
 │   │ 270_claude-sync-  │  │ ra-claude-sync.service │                   │
 │   │ pull.sh           │  │ (systemd → gcloud      │                   │
 │   │ (rsync merge in)  │  │  storage rsync out)    │                   │
 │   └───────────────────┘  └────────────────────────┘                   │
 │             │                  │                                      │
 └─────────────┼──────────────────┼──────────────────────────────────────┘
               │ workload-identity SA (roles/storage.objectAdmin on bucket)
               ▼                  ▼
 ┌─────────────────────────────────────────────────────────────────────┐
 │  GCS bucket: ra-<project-id>-<user-hash>  (shared with other plugins)│
 │                                                                     │
 │  claude-sync/workstations/<ws-id-A>/                                │
 │  ├── projects/<cwd>/<sessionId-A>.jsonl                             │
 │  ├── projects/<cwd>/memory/*.md                                     │
 │  ├── history.jsonl                                                  │
 │  └── claude.json                                                    │
 │  claude-sync/workstations/<ws-id-B>/...                             │
 │  claude-sync/workstations/<ws-id-C>/...                             │
 │  claude-sync/_manifest/<ws-id>.json   (last-push timestamp)         │
 └─────────────────────────────────────────────────────────────────────┘
               ▲                  ▲
               │ same pattern     │
 ┌─────────────┼──────────────────┼──────────────────────────────────────┐
 │             ▼                  ▼                                      │
 │   Workstation B (same components, different namespace under ws-id-B)  │
 └───────────────────────────────────────────────────────────────────────┘
```

---

## Components

### New plugin: `plugins/claude-sync/`

Mirrors the layout of `plugins/github/`. Files:

- **`plugin.yaml`** — config schema:
  - `enabled` (bool, default false)
  - `interval_seconds` (int, default 60)
  - `bucket_name_override` (string, optional — if unset, derived as `ra-<project-id>-<user-hash>`)
  - `sync_all_projects` (bool, default false — at user-level install, auto-sets `bucket_name_override` to the user project's bucket so all GCP projects share one bucket)
  - No secrets — auth is via the existing workstation service account.
- **`Dockerfile.d/110_claude-sync-tools.sh`** — ensures `jq` and `curl` are present (already in base image — script is a `command -v` guard + no-op).
- **`workstation-startup.d/270_claude-sync-pull.sh`** — runs once per boot after `260_claude-setup.sh`. Pulls every namespace under `workstations/` and merges into `/home/user/.claude/`. Idempotent.
- **`workstation-startup.d/271_claude-sync-daemon.sh`** — heredocs the systemd unit + push loop into `/etc/systemd/system/` + `/usr/local/bin/`, then enables/starts the unit. Idempotent.
- **systemd unit** `/etc/systemd/system/ra-claude-sync.service` — runs `/usr/local/bin/ra-claude-sync-loop.sh` as `user`.
- **push loop** `/usr/local/bin/ra-claude-sync-loop.sh`:
  ```bash
  while true; do
    gcloud storage rsync \
      --recursive --delete-unmatched-destination-objects=false \
      --exclude='shell-snapshots/**|sessions/**|plugins/**|cache/**|backups/**|statsig/**' \
      /home/user/.claude/ \
      "gs://${BUCKET}/workstations/${WS_ID}/" || true
    sleep "${INTERVAL}"
  done
  ```

### `ra` CLI changes — **mechanism only, no claude-sync-specific code**

Separation of concerns: the **policy** ("claude-sync needs a bucket called X with role Y bound to env var Z") lives in `plugins/claude-sync/plugin.yaml`. The **mechanism** (the `gcloud storage` shell-outs and the create-time pipeline step) lives in core. Same pattern the codebase already uses for `auth_providers[].iam_roles:` (declared in plugin.yaml, walked generically in `pluginAuthIAMRoles`) and `port-for-tunnel:` (declared in plugin.yaml, served by `Registry.PortByName`). Core has **zero** plugin-specific name hard-codes.

- **`internal/plugins/schema.go`** — new `BucketSpec` field on `PluginSchema`:
  ```yaml
  buckets:
    - name: <logical-handle>
      name_template: "..."        # supports {project_id}, {user_email_hash}
      override_field: <config-field-name>  # optional; if set in plugins.<name>.*, overrides the template
      role: roles/storage.objectAdmin       # IAM role granted on the bucket to the workstation SA
      env_var: RA_PLUGIN_<NAME>_BUCKET      # env var the bucket name is exposed under
  ```
- **`internal/gcs/bucket.go`** — `EnsureBucket`, `ValidateBucketName`, `ResolveBucketName(template, project, userEmail)`, and a generic `GrantRole(r, attempts, delay, notice, bucket, sa, role)`. Generic GCP plumbing, parallel to `internal/secrets/`.
- **`cmd/create.go`** — a single `pluginBuckets(reg.All(), cfg)` helper enumerates every enabled plugin's `BucketSpec` entries (resolved to concrete names). The provisioning step, the IAM bind, and the env injection in `buildContainerEnv` all iterate this list. No string literal "claude-sync" appears anywhere in core.

### `plugins/claude-sync/plugin.yaml` carries the policy

```yaml
name: claude-sync
buckets:
  - name: state
    name_template: "ra-{project_id}-{user_email_hash}"
    override_field: bucket_name_override
    role: roles/storage.objectAdmin
    env_var: RA_PLUGIN_CLAUDE_SYNC_BUCKET
config:
  - { name: enabled, type: bool, default: false }
  - { name: interval_seconds, type: int, default: 60 }
  - { name: bucket_name_override, type: string }
  - { name: sync_all_projects, type: bool, default: false }
```

The runtime contract on the workstation is unchanged: `270_*.sh` and `271_*.sh` read `RA_PLUGIN_CLAUDE_SYNC_BUCKET` exactly as before. Only the *source of truth* for what env var to inject moved from Go into YAML.

### Reused existing utilities

- `internal/gcloud/runner.go` — shell pattern for gcloud calls.
- `internal/gcloud/retry.go` — `RetryOnSANotFound` for IAM bindings against freshly-created SAs.
- `internal/configwrite/append.go` — `MergePluginConfig` already handles registering the plugin's config under `plugins.claude-sync.*`.

---

## Data flow

### Boot sequence (numbers are `workstation-startup.d/` prefixes)

1. `200_remote-agent-setup.sh` (core) — fetches secrets, writes `/run/ra/env`, propagates env to `/etc/environment`.
2. `250_claude-install.sh` (claude plugin) — installs claude on first boot.
3. `260_claude-setup.sh` (claude plugin) — auth setup, seeds onboarding.
4. **`270_claude-sync-pull.sh` (new)** — for each `gs://<bucket>/workstations/<ws-id>/` namespace:
   - rsync **projects/** down → local `~/.claude/projects/`. Session JSONL filenames are UUIDs and don't collide across workstations. CLAUDE.md memory files inside `projects/<cwd>/memory/` are last-writer-wins by mtime (gcloud rsync respects mtime).
   - rsync **todos/**, **plans/**, **session-env/** down — also UUID-keyed, no collisions.
   - For **history.jsonl** and **.claude.json**, only pull from the most-recently-updated namespace (per `_manifest/<ws-id>.json`'s `last_push_at`). This is last-writer-wins across workstations and is acceptable for v1 because the file's contents are predominantly local UI state (numStartups, tipsHistory, growthbook cache) plus onboarding flags.
5. **`271_claude-sync-daemon.sh` (new)** — installs and starts the systemd unit.

### Steady state

- Every 60s, the daemon `gcloud storage rsync`'s the filtered view of `~/.claude/` → `gs://<bucket>/workstations/<this-ws-id>/`, then updates `_manifest/<this-ws-id>.json` with the current timestamp.
- Rsync is incremental: it only re-uploads files whose contents (md5) changed. Steady-state bandwidth is small.

### Shutdown

- No explicit hook. ≤60s of state may be lost if the workstation is killed between cycles. Acceptable since JSONLs are append-only and a new workstation re-syncs to the last persisted offset.

---

## Tech stack

- **Go** — for the small `cmd/create.go` / `internal/gcs/` additions.
- **Bash** — workstation-side pull and push scripts (consistent with existing plugin scripts).
- **systemd** — provides the daemon lifecycle. Cloud Workstations' base image runs systemd inside the container.
- **`gcloud storage rsync`** — the actual sync engine. Already available in the base image (the workstation service account has cloud-platform scope).
- **GCS** — storage backend. One bucket per (project, user). Uniform bucket-level access. Default standard storage class. Versioning off for v1; enable in v2.
- **IAM** — `roles/storage.objectAdmin` granted on the bucket to the workstation SA.

---

## Edge cases

- **Session JSONL collisions**: impossible — claude only writes to its own active session's JSONL (UUID-named).
- **CLAUDE.md memory file collisions** (same `projects/<cwd>/memory/notes.md` edited on two workstations simultaneously): last-writer-wins by mtime. v1 documents this; v2 can surface conflicts via `ra claude sync status`.
- **`.claude.json` divergence** (numStartups, tipsHistory counters drift per workstation): last-writer-wins; not load-bearing — claude regenerates missing fields.
- **Workstation killed mid-rsync**: `gcloud storage rsync` uploads whole files (not atomic across the set), so the destination namespace may be partially updated. Acceptable: next cycle reconciles. Pull-side merge tolerates partial namespaces.
- **Secrets leaking into transcripts**: session JSONLs may contain shell output, tool outputs, and pasted content. The bucket inherits project-level encryption and is IAM-locked to the workstation SA + project owners. Document this risk in the plugin README; recommend CMEK in v2.
- **Bucket name globally unique**: derive as `ra-<project-id>-<sha1(user-email)[:8]>` to avoid collisions and not leak identifying info. Allow override via `bucket_name_override`. The name intentionally omits "claude" so other plugins can share the same bucket under their own prefixes.
- **Stale namespaces from deleted workstations**: linger in GCS forever in v1. Document `gcloud storage rm -r gs://.../workstations/<old-ws-id>/` as the manual cleanup. v2 adds a lifecycle rule.
- **`claude-sync` enabled but `claude` plugin missing**: documented in the plugin README. v1 noops if `/home/user/.claude/` doesn't exist.
- **Concurrent claude write + rsync read**: `gcloud storage rsync` does a streaming read of each file; a partial write could ship a torn JSONL. Acceptable for v1; next cycle overwrites.
- **First-ever boot, empty bucket**: pull is a no-op. Push initializes the namespace.
- **Bucket creation race when two workstations boot simultaneously**: `gcloud storage buckets create` is idempotent against describe pre-check.

---

## Scaling strategy

- **Storage**: ~10–50 MB per workstation; 100 MB-ish total per user. Negligible at GCS pricing (~$0.02/GB/mo).
- **Sync frequency**: 60s default; configurable per plugin install. Users running interactive long sessions can lower to 10s.
- **Bandwidth**: rsync only reships changed files. Once warm, an active 60-min claude session pushes maybe 1–5 MB total.
- **Workstation count**: pull-merge cost is O(N × files). At 10 workstations × 1000 files, pull takes <30s on cold boot. Beyond ~50 workstations, switch to manifest-driven incremental pull (v2).
- **Per-user, per-project isolation**: one bucket per project per user keeps blast radius small.

---

## Possible bottlenecks

- **`gcloud storage rsync` cold-start latency** (~1–2s per invocation): noticeable if interval shrinks below 10s. Mitigation in v2: long-lived Go daemon using the GCS client library + inotify.
- **JSONL growth on long-running sessions**: a 50 MB session file gets re-uploaded whole on every change because rsync compares md5. Mitigation in v2: chunked append-only object writes.
- **Pull-merge cold boot time** at 10+ workstations: linear scan. Mitigation in v2: read `_manifest/*.json` first, skip unchanged namespaces.
- **systemd inside the Cloud Workstations container**: if not available, fall back to a `nohup` background loop launched from `271_*.sh`.
- **gcloud auth at boot**: workstation SA is already active before `271_*.sh` runs (existing `200_remote-agent-setup.sh` uses gcloud successfully).

---

## v2 improvements

- **Real-time sync** via inotify + GCS Go client.
- **Pre-shutdown hook**: systemd `ExecStop=`.
- **CLI surface**: `ra claude sync push|pull|status|diff|reset|gc`.
- **Conflict surfacing**: detect mtime-tied edits and prompt to choose.
- **Team sharing**: `shared/` prefix + per-user IAM grants.
- **Lifecycle / GC**: bucket lifecycle rule auto-deletes namespaces 30 days after last update.
- **Encryption**: CMEK on the bucket; client-side encryption for high-sensitivity workstations.
- **Selective sync**: per-project include/exclude patterns.
- **Backend abstraction**: swap GCS for S3/R2 via a `backend:` field.
- **Versioning**: enable GCS object versioning + `ra claude sync history <file>`.
- **Compaction**: nightly roll-up of `history.jsonl` into `_consolidated/`.

---

## Verification

Manual end-to-end after the v1 build is in place:

1. `make build && make install`.
2. `ra plugin install ./plugins/claude-sync` (level: user).
3. `ra create ws-alpha` — confirm bucket `ra-<project>-<hash>` appears in GCS console, and the workstation SA has `objectAdmin` on it.
4. `ra connect ws-alpha`, run `claude -p "hello"`, observe `~/.claude/projects/<cwd>/<sessionId>.jsonl` filling.
5. Wait ~70s, run `gcloud storage ls gs://<bucket>/claude-sync/workstations/ws-alpha/projects/...` — confirm the JSONL is there.
6. `ra create ws-bravo` from another terminal. Once it boots, `ra connect ws-bravo` and confirm `~/.claude/projects/<cwd>/<sessionId>.jsonl` from ws-alpha is present locally.
7. Run claude on ws-bravo in the same project. Wait 70s. On ws-alpha, restart the workstation (or manually re-run `/etc/workstation-startup.d/270_claude-sync-pull.sh`); confirm ws-bravo's session JSONL is now present.
8. `go test ./internal/gcs/... ./internal/scaffold/...` for the new helper + scaffold tests.
9. (Optional) Validate filter: write `/home/user/.claude/shell-snapshots/test.sh`, wait 70s, confirm it does NOT appear in GCS.
