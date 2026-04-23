# Drop `GH_TOKEN` env var — design

**Date:** 2026-04-23
**Status:** Approved

## Motivation

The workstation entrypoint currently fetches the GitHub PAT from Secret
Manager and writes it to `/etc/environment` as `GH_TOKEN=...`. Two
problems with that:

1. **Boot-order race.** `/etc/environment` is consumed by `pam_env` only
   at PAM-mediated login. code-oss starts before `entrypoint.sh` finishes
   writing the file, so terminals spawned from the IDE inherit code-oss's
   pre-secrets env and never see `GH_TOKEN`. Result: `gh` CLI calls fail
   with `gh auth login` errors inside the IDE, even though the secret is
   on disk.
2. **Plaintext secret in a world-readable file.** `/etc/environment` is
   mode 0644. The PAT is recoverable by anything that can read root-owned
   non-secret files.

The repo already has a working precedent for "fetch the secret on demand
instead of caching it in env": the per-operation git credential helper at
`/usr/local/bin/git-credential-gcp-secret`. Apply the same pattern to
`gh`.

## Goals

- Eliminate `GH_TOKEN` as a workstation env var.
- `gh` continues to authenticate transparently — no `gh auth login` step,
  no manual sourcing.
- PAT rotation propagates immediately on next `gh` invocation.
- Secret never persists on disk under user or root ownership.

## Non-goals

- Changing how Claude Code authenticates (`CLAUDE_CODE_OAUTH_TOKEN`
  remains in `/etc/environment` and `/etc/profile.d/claude-auth.sh` —
  separate problem, separate plan).
- Caching `gh` tokens between invocations.
- Changing the git credential helper or its install path.

## Design

### `gh` wrapper script

A new script at `/usr/local/bin/gh` shadows `/usr/bin/gh` (PATH order
prefers `/usr/local/bin`). On every invocation it fetches the current
PAT from Secret Manager, sets `GH_TOKEN` for the child process only, and
exec's the real `gh`:

```bash
#!/bin/bash
# Auto-generated at workstation boot. Do not edit.
# Fetches the current GitHub PAT from Secret Manager and forwards to
# the real gh CLI. Token never persists; rotations are picked up on
# the next invocation.
GH_TOKEN=$(gcloud secrets versions access latest \
    --secret='${GITHUB_PAT_SECRET_NAME}' \
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || true
exec env GH_TOKEN="${GH_TOKEN}" /usr/bin/gh "$@"
```

`${GITHUB_PAT_SECRET_NAME}` and `${GCP_PROJECT_ID}` are substituted at
install time by `entrypoint.sh` — the wrapper does not depend on those
env vars being set in the caller's environment. Same convention as
`apiKeyHelper` and the git credential helper.

Mode `0755`. Owned by root.

### Entrypoint changes (`.ws/entrypoint.sh`)

Replace the existing `GH_TOKEN`-to-`/etc/environment` block (currently
lines 283–297) with a block that materializes the wrapper:

- Keep `sed -i '/^GH_TOKEN=/d' /etc/environment` so any prior install's
  stale `GH_TOKEN=` line gets removed on first boot after deploying this
  change.
- Drop the `echo "GH_TOKEN=${GH_PAT}" >> /etc/environment` and the
  `GH_PAT=$(gcloud secrets versions access ...)` fetch in the entrypoint
  itself — the secret no longer needs to flow through entrypoint.
- Heredoc the wrapper to `/usr/local/bin/gh`, `chmod 0755`.
- Sanity check: invoke `/usr/local/bin/gh auth status` once. If it
  doesn't exit 0, log a `WARNING` (parallel to the existing helper
  sanity checks at `entrypoint.sh:154` and `:278-281`).
- Also remove the matching `sed` cleanup line at `entrypoint.sh:137`
  (the one inside the Claude auth pre-cleanup) — that line was deleting
  `GH_TOKEN=` from `/etc/environment` before the OAuth code re-wrote it,
  but with this change there's no rewrite, and the `sed` belongs in the
  new `gh`-install block instead, where it's contextually clear.

### Documentation (`README.md`)

- "How auth actually works at runtime" section: add a **GitHub `gh` CLI**
  subsection, parallel to the existing **GitHub PAT** (git credential
  helper) and **Claude API key / OAuth** subsections. Two short
  paragraphs: where the wrapper lives, what it does, that rotation is
  immediate, no env var on the workstation.
- "Security notes" section: drop any reference to `GH_TOKEN` being in
  workstation env. Note that the `gh` wrapper, like the git credential
  helper, fetches per-operation and never persists the value.

### Failure modes

| Scenario | Behavior |
|---|---|
| gcloud fetch returns empty (SA missing `secretAccessor`, secret deleted) | `GH_TOKEN` is empty in the exec env. Real `gh` runs unauthenticated and emits its standard "not logged in" error. Boot-time sanity check would have already logged a WARNING. |
| gcloud not on PATH | `gcloud` invocation fails inside the wrapper, `GH_TOKEN` empty, same as above. Cannot happen in practice (gcloud is in the base image). |
| User runs `gh` before entrypoint completes | `/usr/local/bin/gh` doesn't exist yet, shell falls through to `/usr/bin/gh`, which fails with "no auth". User retries after a few seconds. Acceptable — no worse than the boot-order race the env-var design currently has. |
| User invokes `/usr/bin/gh` explicitly | Bypasses the wrapper, fails with "no auth". Working-as-designed. |
| PAT rotated mid-session | Next `gh` invocation fetches the new value transparently. |

### Out of scope

- `CLAUDE_CODE_OAUTH_TOKEN` remains in `/etc/environment` and
  `/etc/profile.d/claude-auth.sh`. Same boot-order race, same plaintext
  exposure. Not addressed by this plan.
- The boot-order race itself (code-oss starts before entrypoint
  finishes) is not fixed. This plan makes `gh` immune to it; other
  consumers of `/etc/environment` remain affected.

## Test plan

- After deploying: `which gh` → `/usr/local/bin/gh`.
- `printenv GH_TOKEN` → unset (no env var anywhere).
- `grep ^GH_TOKEN= /etc/environment` → no match.
- `gh pr list` succeeds without a `gh auth login` step.
- `gh auth status` reports the PAT as the active credential.
- Rotate the PAT secret to a new version, run `gh pr list` again — picks
  up the new value with no restart, no resourcing.
- Boot-time sanity check: simulate broken SA permissions (revoke
  `secretAccessor` temporarily), reboot the workstation, confirm the
  entrypoint logs a WARNING and the wrapper still installs.
