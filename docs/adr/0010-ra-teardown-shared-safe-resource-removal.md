# ADR 0010: `ra teardown` — reverse-order resource removal with a shared-vs-per-project split

- **Status:** Accepted
- **Date:** 2026-07-07
- **Deciders:** @adamtait

## Context

`ra create` provisions a full stack of GCP resources (Artifact Registry repo,
Secret Manager secrets, service account + IAM, workstation cluster/config/
instance) but there was **no inverse operation**. `ra delete` removes a single
workstation *instance* only; nothing removed the cluster, config, service
account, repo, or secrets.

The gap has a direct, recurring dollar cost. A Cloud Workstations **cluster**
accrues a fixed control-plane fee of **$0.20 per cluster-hour (~$144/month)**,
billed 24/7 regardless of whether any workstation is running — the only way to
stop it is to delete the cluster. Observed billing showed ~$9/day from clusters
with zero running workstations (roughly two idle clusters). Until now, stopping
the fee meant hand-running a dozen `gcloud` commands in the correct dependency
order.

### The subtlety that shapes the whole design

`ra create` names most resources from **user-level defaults** that are **shared
across every project** on the machine:

- cluster → `seedClusterName = "ra-dev-cluster"`
- service account → `seedServiceAccountName = "ra-workstation-sa"`
- Artifact Registry repo → `seedRepoName = "ra-workstation-images"`
- secrets → declared at user level in `~/.config/ra/config.yaml`

Only the workstations **config** (auto-generated per project, see
[`cmd/create.go` config_name logic][create]) and its **workstation instances**
are per-project. Critically, `create` provisions the shared resources
**skip-if-exists** — that is what makes them safe to share: the second project
to run `ra create` reuses the cluster/SA/repo the first one made.

A naïve "reverse of create" teardown is therefore **not** symmetric. `create`
is *skip-if-exists* (safe to share); a literal inverse is *delete-if-exists*
(**unsafe** to share). Running such a teardown from project A would delete the
cluster, service account, repo, and secrets that projects B and C are still
using, silently breaking them. This asymmetry is the core problem this ADR
resolves.

### Constraints

- **Shell out to `gcloud`, no GCP Go SDK** ([CLAUDE.md][claude]). All deletes go
  through the existing `gcloud.Runner`.
- **Idempotent / re-runnable.** A partially-completed teardown must converge to
  a clean state on re-run, so the command cannot hard-fail on an
  already-deleted resource.
- **Destructive → explicit confirmation.** Per the global safety rule, deletes
  require a confirmation gate (or `--force`), mirroring `ra delete`.
- **Some resources must never be casually destroyed:** enabled project APIs are
  project-wide and unsafe to disable; GCS bucket contents (e.g. `claude-sync`
  work artifacts) are unrecoverable.

[create]: ../../cmd/create.go
[claude]: ../../CLAUDE.md

## Decision

Add `ra teardown` that deletes resources in **reverse dependency order** while
splitting them into two classes:

**Per-project (always deleted):**
1. workstation instances (all of them under the config — they accumulate, one
   per `ra create`)
2. the workstations config

**Shared (deleted only when safe):**
3. the cluster · 4. project IAM bindings · 5. the service account ·
6. secrets · 7. plugin GCS buckets · 8. the Artifact Registry repo

Shared resources are deleted **only when this is the last config on the
cluster** — detected by listing the cluster's configs (`Client.ListConfigs`)
and checking that none remain besides ours — **or** when `--delete-shared` is
passed to force it. When other configs remain, the shared steps are skipped and
the plan/output explains why. This preserves the cost goal (tearing down your
*last* project deletes the cluster and stops the fee) while making teardown safe
by default.

Supporting decisions:

- **Check-then-delete.** Each resource is probed (`describe`/`Check`) before
  deletion — the same idiom `create` uses for skip-if-exists. Absent resources
  are skipped (clean idempotency; re-runs converge to exit 0); a delete that
  fails on a resource that *exists* is a real error.
- **Best-effort with aggregation.** A failed delete is collected and surfaced at
  the end via `errors.Join`; it does not strand the remaining steps (so one
  stuck resource can't keep the expensive cluster alive).
- **APIs left enabled** (no `--disable-apis`); **buckets opt-in** via
  `--delete-buckets` (contents unrecoverable); **secrets** skippable via
  `--keep-secrets`. `--dry-run` prints the plan and deletes nothing; `--force`
  skips the prompt; `--project/--cluster/--config` overrides are shared with the
  other commands.
- **No drift with `create`.** The base IAM role list (`baseProjectRoles`), the
  service-account-email formula (`serviceAccountEmailFor`), and the confirmation
  prompt (`confirmYes`) are single definitions shared by `create`/`delete`/
  `teardown`, so the two sides of the lifecycle cannot diverge (e.g. a new
  always-on role added to `create` is automatically removed by `teardown`).

Deleting the cluster is what actually stops the control-plane fee.

## Alternatives Considered

### 1. Literal delete-if-exists inverse of `create` (the first implementation)
Rejected — it was the bug. It deleted the shared cluster/SA/repo/secrets
unconditionally, so a teardown from one project destroyed sibling projects'
workstations. This was caught in code review and drove the per-project/shared
split above.

### 2. Never delete shared resources (only config + instances)
Rejected — defeats the entire purpose. The cluster *is* the control-plane fee;
a teardown that never deletes it can't stop the cost. The last-config detection
lets us delete it exactly when it is safe.

### 3. Rely on `gcloud workstations clusters delete` cascading
Rejected. Even if cluster deletion cascades to child configs/instances, it does
so **across all projects** on the shared cluster (same cross-project destruction
as #1), and it yields no per-resource progress or existence-based idempotency.
Explicit reverse-order deletion is safer and more legible.

### 4. Ownership records/labels marking which project provisioned a resource
Considered — the "correct" general answer would be for `create` to label each
shared resource with a provisioner/refcount and for `teardown` to consult it.
Rejected as too heavy for the need: it changes `create`'s write path and adds
state to reason about. Listing the cluster's remaining configs is a good-enough
proxy for "is anyone else still using this," with no new state and no `create`
changes. If richer sharing (independent SAs/repos per project) ever lands, an
ownership marker can supersede this.

### 5. Also disable the APIs `create` enabled
Rejected. `services enable` is project-wide; other resources or tools in the
same project may depend on `iam`/`secretmanager`/`artifactregistry`. Disabling
is not a safe inverse of enabling, so APIs are deliberately left on.

### 6. Parse `gcloud` stderr for `NOT_FOUND` instead of check-then-delete
Rejected as brittle. Matching error text varies across gcloud versions. A
`describe` existence probe before each delete is the same mechanism `create`
already relies on and cleanly distinguishes "already gone" (skip) from "exists
but failed to delete" (real error).

## Consequences

### Positive
- Closes the `create` → `teardown` lifecycle in one command; the control-plane
  fee can be stopped without hand-running `gcloud`.
- **Safe by default across projects:** tearing down one project never deletes
  shared infrastructure that siblings still use; the cluster is removed only
  when it is genuinely the last consumer (or explicitly forced).
- Idempotent and re-runnable — a partial teardown converges on re-run.
- `create`/`teardown` share role/email/prompt definitions, so the two halves
  of the lifecycle can't silently drift.

### Negative
- Teardown issues one extra read (`workstations configs list`) to make the
  shared-safety decision. If that call fails, teardown errs toward *preserving*
  shared infra and instructs the user to pass `--delete-shared` — safe, but it
  can leave shared resources behind after a transient error.
- "Last config on the cluster" is a heuristic for "no longer shared." It does
  not track non-workstation consumers of the shared SA/repo/secrets (there are
  none today, but nothing enforces that).
- With `--keep-secrets` (or preserved buckets), deleting the service account
  leaves a harmless dangling `deleted:serviceAccount:` binding on the retained
  resource. Cosmetic; documented in the Step-5 comment.

### Neutral
- Buckets are gated behind **both** `--delete-buckets` and shared-deletion,
  because a bucket name can be shared across a user's projects; the unrecoverable
  data stays put unless the operator opts in and it is safe.
- The command's step count is fixed at 8 (mirroring `create`'s fixed total);
  skipped phases still print a "(skipped: …)" note so progress stays legible.

## Verification

`go build ./...`, `go vet ./...`, and `go test ./...` pass. Unit tests in
`cmd/teardown_test.go` drive `runTeardown` with a fake `gcloud.Runner` and
assert:

| Test | Asserts |
|---|---|
| `DeletesInDependencyOrder` | instances → config → cluster → project-IAM → SA → secrets → repo, in order; no bucket delete without `--delete-buckets` |
| `PreservesSharedResourcesWhenOtherConfigsExist` | with a sibling config on the cluster, only the config + instances are deleted; cluster/SA/secrets/repo/IAM untouched |
| `DeleteSharedForcesSharedDeletion` | `--delete-shared` deletes the cluster + SA despite a sibling config |
| `KeepSecrets` | `--keep-secrets` deletes no secret; cluster still torn down |
| `DryRunDeletesNothing` | `--dry-run` records zero mutating `gcloud` calls |
| `BestEffortContinuesAfterFailure` | a failed config delete still lets cluster + SA deletes run; an aggregated error is returned |
| `SkipsAbsentResources` | nothing exists → no delete issued, no error (idempotent re-run) |

Manual: `ra teardown --help` and `ra teardown --dry-run` verified against a
configured project. The destructive path was **not** executed against a live
billing project as part of this change; `--dry-run` is the recommended
first step for operators.
