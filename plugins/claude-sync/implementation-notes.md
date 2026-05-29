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

## 2026-05-28 — Fix: setsid fallback when systemd is not PID 1

### What was broken

On every workstation provisioned since claude-sync was installed, the
push loop **never started**. The boot-time pull script ran once
correctly. The systemd unit file and the loop binary were materialized
on disk. But `gcloud storage ls gs://<bucket>/claude-sync/workstations/`
returned **zero objects** across the entire fleet — no workstation has
ever pushed to GCS.

### Root cause

The Cloud Workstations default base image runs
`/google/scripts/entrypoint.sh` as PID 1, **not systemd**:

```
$ systemctl is-active ra-claude-sync
System has not been booted with systemd as init system (PID 1).
Failed to connect to bus: Host is down
```

`271_claude-sync-daemon.sh` called `systemctl daemon-reload && systemctl
enable --now ... && systemctl restart ...`, all of which **failed
silently** because of the trailing `|| true`. The unit file was on disk
but no init system ever launched it. The pull side (270) ran fine
because it executes directly during boot, not via systemd.

`SPEC.md` anticipated this exact failure mode in "Possible bottlenecks":
> **systemd inside the Cloud Workstations container**: if not available,
> fall back to a `nohup` background loop launched from `271_*.sh`.

The fallback was never implemented.

### Decision

Detect PID 1 in `271_*.sh` and pick the launch strategy at runtime:

- `[ "$(cat /proc/1/comm)" = "systemd" ]` → existing systemd path
  (preserves correct behavior on any future image that does boot systemd).
- otherwise → `setsid --fork "${LOOP_PATH}" </dev/null`, mirroring the
  marimo plugin's daemon-launch pattern (`plugins/marimo/workstation-startup.d/250_marimo-start.sh:75-79`).

Idempotency in the fallback path: a pidfile at `/run/ra-claude-sync.pid`,
which the loop writes (`echo $$ > "${PID_FILE}"`) as one of its first
actions and clears via an `EXIT` trap. 271 reads that file and `kill
<pid>`s the previous loop with a 5s SIGTERM grace before SIGKILL — no
pattern matching, no risk of collateral kills. (The first draft used
`pgrep -f`; see the "Pidfile-based idempotency" tradeoff below for why
it was abandoned.) Re-running 271, or rebooting the workstation, picks
up env changes from `ra create` without spawning duplicate loops.

The pidfile lives in `/run` so it auto-clears on reboot — any leftover
PID would refer to a vanished process anyway, and the `kill -0` liveness
check before SIGTERM means a stale file is a no-op rather than an error.

### Tradeoffs

**No supervisor in the setsid path.** Under systemd, `Restart=always`
would respawn the loop after any death. Under setsid, if the loop
exits — e.g., a kernel signal, or any of its three startup `exit 1`
branches — it stays dead until the next workstation reboot re-runs
271. Considered acceptable because:

- All three `exit 1` branches in the loop are preconditions that 271
  itself re-validates before launching the loop. If 271 launched the
  loop, those checks pass, so the early exits don't fire.
- The loop body wraps every `gcloud` call in `|| true`, so transient
  gcloud failures never kill the loop.
- A real death (signal, OOM) on a Cloud Workstation is rare; reboot
  recovery is well-established (this is how the original broken
  systemd path also "recovered" — via reboot).
- Adding a supervisor wrapper would be ~20 LOC and duplicate
  systemd's job for what is effectively a corner case.

If we observe production loop deaths, the next step is a small
respawn wrapper in 271 (`while true; do setsid --fork ...; sleep 60;
done` style), not changing the loop body.

**Two-path complexity.** The script now branches on systemd
availability. Could simplify by deleting the systemd path entirely
(it's dead code on the current image), but keeping it costs nothing
and preserves correct behavior if Cloud Workstations ever switches
to systemd-as-PID-1 (which is the documented default for many of
their newer image variants).

**Pidfile-based idempotency (not `pgrep -f`).** First draft used
`pgrep -f "ra-claude-sync-loop\.sh"` / `pkill -f` to find and stop
existing loops. Live-tested it on the broken workstation and noticed
the regex also matches the **calling shell's own command line**
whenever that shell happens to mention the loop path (a developer who
just `cat`ed the script, or any shell whose argv contains the string).
A `pkill -f` from inside 271 would therefore SIGTERM the calling shell.

Switched to a pidfile (`/run/ra-claude-sync.pid`): the loop writes its
own PID on start and clears it via an EXIT trap. 271 reads the file
and calls `kill <pid>` directly — no pattern matching, no risk of
collateral kills. `/run` is cleared on every boot so stale pidfiles
auto-clean.

**Migration: one-time double-loop possible.** If someone manually
re-runs the new 271 on a workstation that already has the **old**
loop running (no pidfile written), 271 has no way to discover that
loop and spawns a second one. Acceptable because:
- The intended deploy path is a workstation rebuild, which reboots and
  clears the old loop via OS cleanup.
- The migration window is small and operator-visible
  (`pgrep -af ra-claude-sync-loop`).
- Adding a `pgrep -fx` fallback would re-introduce a more bounded
  version of the same matches-calling-shell risk.

**Launch race between 271 and the loop's first pidfile write.** If 271
is invoked twice in rapid succession (millisecond gap), the second
invocation reads no pidfile yet — the first loop hasn't run `echo $$ >
"${PID_FILE}"` — and spawns a second loop. Last-writer-wins on the
pidfile means future 271 runs will only see the most recent PID; the
older loop becomes an untracked orphan. Unlikely in practice (271 is a
boot-time script invoked once; manual re-runs are seconds apart, not
milliseconds), and the operator can spot it via
`pgrep -af ra-claude-sync-loop`. Closing the race would require
flocking the pidfile path or a synchronous PID-capture wrapper around
`setsid`; neither is worth the complexity for a corner that's near-
impossible to hit non-deliberately.

**Stale-pidfile + PID reuse.** If the loop is SIGKILL'd externally
(skipping the EXIT trap), the pidfile is left behind. On reboot
`/run` is cleared so the stale file vanishes; without a reboot, the
next 271 run reads the stale PID, `kill -0`s it, and if the kernel
has reused that PID for an unrelated process, 271 will SIGTERM the
unrelated process. Generic pidfile concern; mitigating it requires a
comm/cmdline cross-check that brings back the pgrep-pattern issues
this design deliberately avoided. Accepted given:
- The loop's EXIT trap fires on SIGTERM, SIGINT, and clean exit; only
  SIGKILL (or kernel OOM kill) skips it.
- PIDs on Linux range to 4 million by default; reuse within seconds
  of a SIGKILL on a single-tenant workstation is unlikely.
- The kill target is a single integer read from a root-only-writable
  file, so the threat model is operator error, not adversarial.

### Verified live

Reproduced the original "bucket is empty" bug on a Cloud Workstation
running PID 1 = `entrypoint.sh`. With this fix applied:
- `sudo bash 271_claude-sync-daemon.sh` → setsid branch fires, loop
  starts, pidfile written.
- Within seconds, `gcloud storage ls gs://<bucket>/claude-sync/workstations/`
  shows the workstation's namespace populated for the first time.
- Re-running 271 cleanly stops the old loop and starts a new one;
  exactly one `ra-claude-sync-loop.sh` process remains afterwards.
- `_manifest/<ws>.json` records the freshest push timestamp.

### What was NOT changed

- The loop script body. It's identical to the previous version, and
  is correct under both launch strategies. Its `exit 1` semantics
  match systemd's restart policy; under setsid those exits are
  near-unreachable as noted above.
- The systemd unit file content. Same.
- The Dockerfile.d/110_*.sh "WARN if systemctl missing" check. The
  `systemctl` binary is still present on the image (the failure mode
  is "systemd is not PID 1", not "systemctl is missing"), so the
  soft-assert remains meaningful for environments where someone
  builds against a stripped image.

---

## 2026-05-22 — Generic bucket name + plugin-namespaced paths + `sync_all_projects`

### Bucket name: removed "claude" from template

**Decision:** Changed `name_template` from `"ra-claude-{project_id}-{user_email_hash}"` to `"ra-{project_id}-{user_email_hash}"`.

**Why:** The bucket is now intended to be shared across plugins. A name that embeds "claude" implies exclusive ownership and would be misleading if, say, a `github-sync` plugin also writes to it.

**Tradeoff:** Existing deployments that already provisioned a `ra-claude-*` bucket will not automatically migrate. Users upgrading will get a new bucket on the next `ra create`. Their data in the old bucket is orphaned unless they manually copy it or set `bucket_name_override: ra-claude-<proj>-<hash>` in their config to keep using the old bucket. This is a one-time migration cost; new installs are unaffected. Documented in this note rather than adding migration code since the plugin is young and production deployments are few.

**What was NOT changed:** The systemd service name (`ra-claude-sync.service`), the log file path (`/var/log/ra-claude-sync.log`), and the push loop binary (`/usr/local/bin/ra-claude-sync-loop.sh`) all still include "claude-sync" — these are internal workstation artifact names scoped to this plugin, not global GCS namespace identifiers, so keeping them claude-sync-specific is correct.

---

### GCS paths: plugin-namespaced under `claude-sync/` prefix

**Decision:** All GCS paths inside the bucket are now prefixed with `claude-sync/`. Previously data lived at `gs://bucket/workstations/{ws-id}/...` and `gs://bucket/_manifest/...`. Now it's `gs://bucket/claude-sync/workstations/{ws-id}/...` and `gs://bucket/claude-sync/_manifest/...`.

**Why:** Without a prefix, the top-level bucket namespace is unpartitioned. A second plugin that writes `workstations/` or `_manifest/` objects would collide. The `claude-sync/` prefix acts as a namespace that is easy to read in GCS console and unambiguous in `gsutil`/gcloud commands.

**Implementation note on 270_claude-sync-pull.sh:** Introduced a `PLUGIN_PREFIX="gs://${BUCKET}/claude-sync"` variable to avoid repeating the string. The `NAMESPACES` array is populated by `gcloud storage ls "${PLUGIN_PREFIX}/workstations/"` so each `${ns}` URL already carries the full path — the `rsync` calls inside the loop (`${ns}${sub}/`) still work without change.

**Tradeoff:** Same migration issue as the bucket rename — existing data under the old paths is stranded. Same mitigation (manual copy or `bucket_name_override` to keep the old bucket entirely).

---

### `sync_all_projects` config option

**Decision:** Added `sync_all_projects: bool` (default: false) to `plugin.yaml`. When `true` at user-level install and `bucket_name_override` is not already set, a post-prompt step in `cmd/plugin.go` resolves `ra-{user_project_id}-{user_email_hash}` from the user-level config and writes it into `res.Config["bucket_name_override"]` before `writePluginAndSecrets` persists it.

**Why:** Loading the user config (`config.UserConfigPath()` + `config.LoadUserConfig()`) directly — rather than using the already-merged `cfg` — ensures we get the user's home project ID even when `ra plugin install` is invoked from a project directory whose `.ra/config.yaml` overrides `project_id`. The merged config could be a project-specific project, not the user's intended home project.

**Tradeoff:** We use `UserConfig.Git.UserEmail` from the user config. If the user's email differs between user and project configs, the derived bucket name uses the user-level email. This is the right behavior (we want a stable, user-anchored identifier), but it means the bucket for `sync_all_projects` may differ from the per-project bucket even in the same GCP project if emails diverge.

**What happens at project-level install:** The `if level == "user"` guard means `sync_all_projects=true` at project level silently stores `true` in config but never auto-derives `bucket_name_override`. The plugin still resolves the template (`ra-{project_id}-{user_email_hash}`) at `ra create` time. This is intentional: project-level installs are expected to be project-specific, and the user can always set `bucket_name_override` manually if they want cross-project sharing from a project install.

**Cross-project IAM:** When `sync_all_projects=true` and workstations in project B run `ra create`, `gcs.EnsureBucket` is called with `bucket_name_override` pointing at a bucket owned by project A. The `gcloud storage buckets describe` check succeeds globally (GCS bucket names are unique; the describe command returns success if the caller has read access, regardless of owning project). The bucket creation is skipped. `gcs.GrantRole` then grants project B's workstation SA `objectAdmin` on the bucket — this is a bucket-level IAM grant that works cross-project. No changes to `cmd/create.go` or `internal/gcs/` were needed.

---

## 2026-05-21 — Refactor: removed all claude-sync-specific code from cmd/create.go

**Decision:** Reviewing PR #126 surfaced an architectural smell — the
initial implementation embedded claude-sync-specific knowledge (helper
functions named `claudeSyncEnabled` / `claudeSyncBucketName`, a literal
`RA_PLUGIN_CLAUDE_SYNC_BUCKET` env-var key, and a hard-coded "claude-sync
disabled" placeholder step) inside `cmd/create.go`. That violated the
"ra ships with no built-in plugins" principle from CLAUDE.md.

Refactored to a generic `buckets:` field on the plugin schema (parallel
to `auth_providers[].iam_roles:`):

- `internal/plugins/schema.go`: new `BucketSpec` field on
  `PluginSchema`, validated in `LoadPlugin`.
- `internal/gcs/bucket.go`: added `ResolveBucketName(template, project,
  email)` template resolver; renamed `GrantObjectAdmin` →
  `GrantRole(role)` so plugins declare their own role.
- `cmd/create.go`: a single `pluginBuckets(reg, cfg)` helper enumerates
  every enabled plugin's bucket needs. The pipeline step, the IAM bind,
  and the env injection in `buildContainerEnv` all iterate this list.
  `grep -i claude-sync cmd/create.go` returns **zero matches**.
- `plugins/claude-sync/plugin.yaml`: gained a `buckets:` declaration —
  the sole source of truth for what bucket claude-sync needs.

**Why:** The codebase already has the right shape for plugin-declared
GCP needs (`iam_roles:`, `port-for-tunnel:`). Bucket provisioning should
follow the same pattern: policy in plugin.yaml, mechanism in core. The
next plugin that needs a GCS bucket adds zero lines to `cmd/create.go`.

**Tradeoff:** Adds ~120 LOC of generic plugin-buckets handling (Go +
tests); deletes ~80 LOC of claude-sync-specific code. Net +~40 LOC but
the architectural quality is meaningfully better. The runtime contract
on the workstation is unchanged — `RA_PLUGIN_CLAUDE_SYNC_BUCKET` is
still injected, the boot scripts still read it. Only the *source of
truth* for what env var to inject moved from Go into YAML.

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
