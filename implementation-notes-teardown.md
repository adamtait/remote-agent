# `ra teardown` — implementation notes

Running log of decisions, deviations from the plan, and tradeoffs made while
implementing the `ra teardown` command. See
`/home/user/.claude/plans/snug-weaving-badger.md` for the approved plan.

> **Note on filename:** the repo already has a gitignored `implementation-notes.md`
> from a previous task (open-sourcing `ra`). To avoid clobbering it, these notes
> live in `implementation-notes-teardown.md`.

## Decisions not fully specified in the plan

### 1. Cluster deletion order vs. cascade
The plan deletes workstations → config → cluster explicitly. `gcloud workstations
clusters delete` *can* cascade, but it refuses (or is slow/ambiguous) with children
present, and explicit ordering gives clean per-resource progress output. Kept the
explicit reverse order. **Tradeoff:** more gcloud calls, but idempotent and legible.

### 2. Best-effort vs. fail-fast
Teardown continues past individual failures (e.g. a resource already deleted) and
returns an aggregated error at the end. This makes re-runs safe (idempotent cleanup)
and prevents one missing resource from stranding the other steps. A hard fail-fast
would leave the expensive cluster alive if, say, an earlier secret delete errored.

### 3. NOT_FOUND detection → check-then-delete (revised)
`gcloud ... delete` on a missing resource exits non-zero. Regex-matching stderr for
"NOT_FOUND" is brittle across gcloud versions. **Chosen approach: existence probe
(`describe`/`Check`) before each delete** — the same Check-then-act idiom `ra create`
uses. If a resource is absent we skip it (clean idempotency; re-runs converge to
exit 0); if it exists but the delete fails, that's a *real* error we collect and
surface (so a genuine failure to delete the costly cluster isn't masked). Cheaper and
more robust than parsing gcloud error text.

**Exception — project IAM bindings:** there's no cheap per-binding existence check,
and `remove-iam-policy-binding` on an absent binding is harmless. Those removals use
`RunSoft` (best-effort, logged at debug) rather than check-then-delete.

### 4. Which resources get explicit IAM cleanup
Only **project-level** bindings are removed explicitly (`projects
remove-iam-policy-binding` for `roles/logging.logWriter`,
`roles/monitoring.metricWriter`, plus any plugin auth-provider roles — computed with
the same `pluginAuthIAMRoles`/`appendUniqueRoles` helpers `create` uses, so the sets
stay in sync). Secret-, bucket-, and Artifact-Registry-level accessor bindings are
**not** removed explicitly — deleting the service account (or the parent resource)
invalidates them. This avoids a dozen redundant `remove-iam-policy-binding` calls.

### 5. APIs are never disabled
`ra create` enables workstations/artifactregistry/secretmanager/iam APIs. Teardown
leaves them enabled: they're project-wide, other resources/tools may depend on them,
and disabling is not a safe inverse of enabling. Documented in `--help` and the plan.
No `--disable-apis` flag was added (could be a future opt-in).

### 6. Step count is fixed at 8
Mirrors `create`'s fixed `totalCreateSteps`. Conditional phases (`--keep-secrets`,
buckets-without-`--delete-buckets`) still consume their step number and print a
"(skipped: …)" note, so progress output stays legible and the numbering is stable.

### 7. `--dry-run` still calls `client.List`
Dry-run previews the actual workstation instances, so it issues the read-only
`workstations list` (via the runner's `Output`, not `Run`). It performs **zero**
mutations — the test asserts `len(runs) == 0`. Only `List`/`Check`-style reads happen.

### 8. Reused the existing `testRunner`
`cmd/resolve_test.go` already defines a package-level `testRunner` fake implementing
`gcloud.Runner`. teardown_test.go reuses it rather than defining a second fake. One
wrinkle: that fake's `RunSoft` delegates to `Run`, so the best-effort project-IAM
removals land in `runs` too — tests account for this (they match on `delete`, not on
every recorded call).

### 9. Notes filename
Kept these notes in `implementation-notes-teardown.md` because a gitignored
`implementation-notes.md` from a prior task already exists; overwriting it would have
destroyed unrelated content.
