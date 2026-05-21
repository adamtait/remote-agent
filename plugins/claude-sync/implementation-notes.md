# Implementation Notes — `claude-sync` plugin

A living log of decisions, deviations from `SPEC.md`, and tradeoffs made while
implementing the plugin. Newest entries at the top.

Format per entry:

```
## <date> — <short title>
**Decision:** ...
**Why:** ...
**Tradeoff:** ...
```

---

## 2026-05-21 — Review feedback applied (PR #126)

Self-review on PR #126 surfaced 8 items. All applied in a follow-up
commit:

1. **`Restart=on-failure` ⇒ `Restart=always`** with
   `StartLimitIntervalSec=600`/`StartLimitBurst=20` plus
   `RestartSec=30`. The loop's env-wait timeout now `exit 1`s so
   systemd actually retries instead of leaving the unit silently
   inactive.
2. **Pull stages into a tmpdir + `cp -an`** rather than rsync-ing
   directly into `~/.claude/`. Newer local files (e.g. unsynced edits
   from before the workstation last restarted) win over the GCS copy
   for projects/, todos/, plans/, session-env/. history.jsonl and
   `.claude.json` keep last-writer-wins via the manifest.
3. **Loop + pull log to `/var/log/ra-claude-sync.log`** (tee for pull,
   `exec >> ... 2>&1` for the loop). Stderr from gcloud calls goes to
   the same file via `2>>"${LOG_FILE}"` so a sync regression leaves an
   auditable trail in steady state, not just /dev/null.
4. **`internal/gcs.ValidateBucketName`** + call site in
   `cmd/create.go` before any GCS-side operation. Rejects user
   overrides that violate GCS naming rules up-front, before AR repo
   creation, so we don't half-provision a workstation. Tests cover the
   regex + the invariant that auto-derived names always pass.
5. **`ts="${ts:-0}"` + numeric-only guard** in the manifest-newest-
   namespace selection loop, so a `gcloud storage cat` failure leaving
   empty stdout doesn't error out the entire pull.
6. **README + ADR**. `plugins/claude-sync/README.md` covers ops,
   security warning about transcript contents in GCS, and
   configuration. `docs/adr/0002-claude-state-sync.md` captures the
   storage layout, per-workstation namespacing, IAM model, and
   alternatives considered.
7. **`Dockerfile.d/110_*.sh` trimmed**: removed the curl install
   branch (Cloud Workstations base image guarantees curl) and removed
   the unconditional apt-get update — `apt-get update` now only fires
   when jq is genuinely missing.
8. **Pull script's `sudo -u user mkdir -p`** dropped; plain `mkdir -p`
   under the existing root context, with the final `chown -R
   user:user` already covering ownership.

---

## 2026-05-21 — Workstation ID discovered at runtime, not injected at create time

**Decision:** The boot scripts compute `WS_ID` themselves (GCE metadata server with
`hostname` as fallback) rather than receiving it via `RA_PLUGIN_CLAUDE_SYNC_WS_ID`
in `--container-env`.

**Why:** `--container-env` is set on the workstations *config*, which is shared
across every workstation instance using that config. A single static value
there would give every instance the same ws-id, defeating per-workstation
namespacing. The workstation name is also only resolved at `cmd/create.go:491`,
*after* `buildContainerEnv` has already produced its KV string at line 429.

**Tradeoff:** Boot scripts now have a small runtime discovery step instead of
just reading an env var. Acceptable: the metadata server is always reachable
from inside a Cloud Workstation, and `hostname` matches the workstation name in
practice (good fallback).

---

## 2026-05-21 — systemd unit and push loop heredoc'd inline by `271_*.sh`

**Decision:** The systemd unit file (`ra-claude-sync.service`) and the push loop
script (`/usr/local/bin/ra-claude-sync-loop.sh`) are emitted by
`workstation-startup.d/271_claude-sync-daemon.sh` via heredocs, rather than
shipped as separate files under `plugins/claude-sync/assets/`.

**Why:** `internal/scaffold/buildcontext.go` only walks the three known plugin
subdirectories: `Dockerfile.d/`, `workstation-startup.d/`, `profile.d/`. An
`assets/` directory would be silently ignored. Adding `assets/` support to the
build-context assembler is out of scope for this PR — the heredoc keeps the
unit definition in source control without touching shared scaffold code.

**Tradeoff:** The unit file is less ergonomic to edit (lives inside a bash
heredoc), and CI tools that lint systemd units won't see it. Acceptable for v1
given the unit is ~12 lines. If we add more units later we should revisit and
extend `buildcontext.go`.

---

## 2026-05-21 — Push uses an allowlist of paths, not a regex exclude

**Decision:** The push loop iterates a fixed allowlist (`projects/`, `todos/`,
`plans/`, `session-env/`, `history.jsonl`, `.claude.json`) and pushes each
explicitly, rather than running one whole-tree `gcloud storage rsync` with
`--exclude` patterns for the volatile paths (`shell-snapshots/`, `sessions/`,
`plugins/`, `cache/`, `backups/`, `statsig/`).

**Why:** `gcloud storage rsync` accepts `--exclude` as a single regex pattern,
which is awkward to compose for directory-tree exclusions across multiple
top-level dirs. The allowlist makes intent obvious: "we sync exactly these six
things, period."

**Tradeoff:** New first-level entries that Claude Code adds in future versions
won't be picked up automatically — someone has to add them to the allowlist.
Acceptable: the set of things worth syncing is small and known, and we'd
want to vet new entries anyway (they may be machine-specific or contain
secrets).

---

## 2026-05-21 — Step 3 always logs even when claude-sync is disabled

**Decision:** When the plugin is not installed/enabled, the new
`ensure_gcs_bucket` step still emits a placeholder `logging.Step` with the
message "claude-sync disabled — skipping GCS bucket" before completing
immediately.

**Why:** `totalCreateSteps` is a constant baked into the binary; if we
*conditionally* log step 3 only when claude-sync is enabled, the visible
step counter jumps from "[2/11]" to "[4/11]" for users without the plugin
— confusing. The placeholder keeps the progress display monotonic.

**Tradeoff:** A no-op step appears for everyone, even users uninterested in
claude-sync. Acceptable — it's a single line, and surfacing that the
feature exists may be useful onboarding signal.

---

## 2026-05-21 — Push loop runs as root, not as `user`

**Decision:** The systemd unit `ra-claude-sync.service` omits `User=`, so it
runs as root.

**Why:** gcloud's application-default credentials in Cloud Workstations come
from the metadata server. Switching users via `User=user` would not break
gcloud auth (the metadata server is reachable by any uid), but root keeps
the unit self-contained: it can read `/home/user/.claude/` regardless of
unusual file modes, and `gcloud storage cp` writes to GCS where local uid
is irrelevant.

**Tradeoff:** Higher privilege than strictly needed. Acceptable: there's no
attack surface the loop adds beyond what `200_remote-agent-setup.sh` already
needs (both fetch secrets / data via gcloud and the workstation SA).

---

## 2026-05-21 — `totalCreateSteps` bumped from 10 to 11

**Decision:** A new pipeline step "Ensure GCS bucket" is inserted between the
Artifact Registry step (was 2) and the build/push step (was 3). Step numbers
3–10 shift to 4–11. `totalCreateSteps` becomes 11.

**Why:** SPEC requires bucket provisioning at create time. The convention in
`runCreate` is one logging.Step per discrete provisioning operation.

**Tradeoff:** The step shift will rebase awkwardly with any concurrent PR also
adding pipeline steps. Acceptable given no concurrent step-adding work is in
flight.

---

## Deferred to v2

Items called out in `SPEC.md`'s v2 section that this PR explicitly does *not*
implement:

- Real-time inotify sync (still 60s polling loop).
- Pre-shutdown systemd `ExecStop=` final flush.
- `ra claude sync` CLI subcommands (push/pull/status/diff/reset/gc).
- Conflict surfacing UI.
- Multi-user team sharing via `shared/` prefix.
- Bucket lifecycle / garbage collection for deleted workstations.
- CMEK / client-side encryption.
- Selective per-project include/exclude.
- Non-GCS backends.
- Object versioning.
- `history.jsonl` compaction.
