# Plan: Headless Claude Code Onboarding Bypass

## Problem

`claude -p` works on the VM using `CLAUDE_CODE_OAUTH_TOKEN`, but interactive
`claude` still hits the onboarding wizard on first run. The wizard launches a
browser-based OAuth flow that cannot complete on a headless workstation. Auth
is fine; the wizard is the blocker.

## Fix (from handoff DOC)

Pre-seed `/home/user/.claude.json` with:

- `hasCompletedOnboarding: true`
- `lastOnboardingVersion` matching installed `claude --version`
- `oauthAccount` block: `{accountUuid, emailAddress, organizationUuid}`

The `oauthAccount` fields must come from a machine where the user has logged
in interactively (the operator's laptop).

## Scope

- Applies only when `CLAUDE_AUTH_MODE=oauth`.
- `apikey` mode is out of scope (uses `apiKeyHelper`; wizard behavior there
  may differ and no user report exists yet).
- Runs at every workstation boot so rotations and upgrades are absorbed.

## Repo context that shapes the design

- `entrypoint.sh` runs as root on every boot and already handles oauth token
  fetch from Secret Manager (lines 112-152).
- User-scoped work uses `sudo -u user` (pattern at lines 233-247).
- `/home/user` is a persistent disk. Existing code intentionally avoids
  single-shot marker files (comment at lines 223-228). Design follows the
  same rule: idempotent, merge-not-clobber.
- `jq` is already installed (Dockerfile line 11).

## Simplest viable version

A new `seed_claude_onboarding()` function inside the existing `oauth)` branch
of `entrypoint.sh`. One new Secret Manager secret holding the `oauthAccount`
JSON. One new flag on `create-workstation.sh` to plumb the secret name. No
new files in the image.

## Architecture

```
One-time (operator, per user)
┌─────────────────────────────┐
│  Operator's laptop          │
│   extract-account.sh ──┐    │
│   (reads ~/.claude.json)    │
│                        │    │
└────────────────────────┼────┘
                         ▼
            ┌────────────────────────┐
            │ GCP Secret Manager     │
            │  ├─ claude-oauth-token │  (existing)
            │  └─ claude-oauth-acct  │  (NEW: JSON blob)
            │  └─ github-pat         │  (existing)
            └────────────┬───────────┘
                         │ fetched every boot via SA
                         ▼
Every boot (workstation VM)
┌────────────────────────────────────────────┐
│  entrypoint.sh (as root)                   │
│    case oauth)                             │
│      1. fetch token   → /etc/environment   │  (existing)
│      2. fetch account → JSON               │  (NEW)
│      3. claude --version (as user)         │  (NEW)
│      4. sudo -u user: merge into           │  (NEW)
│         /home/user/.claude.json            │
│      5. chmod 600, chown user:user         │  (NEW)
└────────────────────────────────────────────┘
                         │
                         ▼
              user runs `claude` → drops straight into session
```

## Components

1. **`entrypoint.sh`** — new `seed_claude_onboarding()` helper invoked at the
   end of the `oauth)` branch.
2. **`create-workstation.sh`** — new `--claude-oauth-account-secret NAME`
   flag, plumbed as env var `CLAUDE_OAUTH_ACCOUNT_SECRET_NAME` on the
   workstation config. Mirrors `--claude-auth-secret`.
3. **`README.md`** — short operator section:
   - one-time laptop `jq` snippet
   - `gcloud secrets create claude-oauth-account --data-file=-`
   - new flag on `create-workstation.sh`

No new standalone scripts shipped in the image.

## Secret schema

Single JSON blob stored verbatim:

```json
{
  "accountUuid": "...",
  "emailAddress": "...",
  "organizationUuid": "..."
}
```

Separate from the token secret for v1: smaller IAM blast radius, matches the
existing one-secret-per-concern pattern. Collapse into a single secret in v2
if it proves painful.

## Data flow (per boot, oauth mode)

```
/etc/environment has CLAUDE_OAUTH_ACCOUNT_SECRET_NAME
        │
        ▼
gcloud secrets access → JSON string
        │
        ▼
validate: jq -e '.accountUuid and .emailAddress and .organizationUuid'
        │
        ▼
VERSION=$(sudo -u user claude --version | awk '{print $1}')
        │
        ▼
if /home/user/.claude.json exists:
    merge with jq: .hasCompletedOnboarding = true
                 | .lastOnboardingVersion = $VERSION
                 | .oauthAccount = $ACCT
else:
    write fresh file with those three keys
        │
        ▼
atomic mv → chown user:user → chmod 600
        │
        ▼
optional: sudo -u user claude -p "respond OK" | grep -q OK
```

## Tech stack

- Bash + `jq` (already installed).
- `gcloud` for Secret Manager (already used).
- `sudo -u user` for any write under `/home/user`.

No new runtime dependencies.

## Step-by-step build order

1. **Schema decision** — separate secret for account metadata. Document the
   JSON shape in README.
2. **Extend `create-workstation.sh`** — add `--claude-oauth-account-secret
   NAME`. Fail fast if `CLAUDE_AUTH_MODE=oauth` and flag is missing. Set
   `CLAUDE_OAUTH_ACCOUNT_SECRET_NAME` on the workstation config.
3. **`seed_claude_onboarding()` in `entrypoint.sh`** — new function, called
   at the end of the `oauth)` branch. Guarded by
   `[[ -n "${CLAUDE_OAUTH_ACCOUNT_SECRET_NAME:-}" ]]`; logs a WARNING and
   continues if unset, so pre-existing workstations keep working on image
   rebuild.
4. **Merge vs create** — mirror the existing settings.json pattern at
   entrypoint.sh:94-108. File missing → write fresh. File exists + `jq`
   present → merge. Else warn.
5. **Atomic write** — `tmp=$(sudo -u user mktemp)`, render with `jq`,
   `sudo -u user mv` into place. Explicit chown, `chmod 600`.
6. **Smoke test** — `sudo -u user bash -c 'claude -p "respond OK" | grep -q
   OK'`. Log WARNING on failure, do not exit; a failed smoke test is
   diagnostic, not fatal (token fetch already succeeded upstream).
7. **README update** — laptop extraction snippet, `gcloud secrets create`,
   new flag usage.
8. **Manual test** — rebuild image, recreate workstation, SSH in, run
   `claude` interactively, confirm no wizard. Run `/status` to confirm
   subscription-based auth.

## Edge cases

- **Existing `/home/user/.claude.json`** — merge, never clobber. User may
  have trusted directories, MCP servers, project state.
- **`claude --version` format drift** — parse defensively (`awk` + regex
  `^[0-9]+\.[0-9]+\.[0-9]+`). On parse failure, skip writing
  `lastOnboardingVersion` rather than writing garbage.
- **Secret has placeholder/empty values** — validate with `jq -e`. Refuse to
  write config.
- **Secret rotated to a different account** — merge overwrites
  `oauthAccount`. Correct: token rotation likely implies account rotation.
- **`CLAUDE_AUTH_MODE=apikey`** — bypass not applied. Extend later if
  reported.
- **Token fetch ok but account fetch fails** — log WARNING, skip onboarding
  write. `-p` still works; interactive will show the wizard. Degraded, not
  broken.
- **Upgrades between boots** — next boot rewrites `lastOnboardingVersion`.
  One wizard firing possible between upgrade and next boot; acceptable.
- **`jq` missing** — already a Dockerfile dep; still check and warn.
- **`/home/user` not yet provisioned** — base image creates it before
  startup scripts; verify with `sudo -u user test -d "$USER_HOME"` and bail
  loudly if not.

## Scaling strategy

- Per user: one pair of secrets (`claude-oauth-token-<user>`,
  `claude-oauth-account-<user>`). `create-workstation.sh` already
  parameterizes per user; no new axis.
- Fleet: same Secret Manager pattern as today. IAM grants `secretAccessor`
  on both secrets to the workstation SA. No coordination.
- No caching: merge runs at boot on persistent disk, <100ms.

## Possible bottlenecks

- **Manual laptop extraction** — acceptable for personal use, a friction
  point for self-service. v2 target.
- **Token ↔ account coupling** — nothing validates they match. If they
  drift (token rotated from a different laptop), wizard re-triggers. Annoying,
  not harmful.
- **Onboarding schema churn** — Anthropic may rename or expand required
  fields. No public contract. Mitigation: `claude doctor`-style check logged
  at boot (v2).

## v2 improvements

- **Fetch account from API** — if/when Anthropic exposes "whoami" for an
  OAuth token, delete the laptop step entirely.
- **Single merged secret** — `{token, accountUuid, emailAddress,
  organizationUuid}` in one JSON blob. One fetch, guaranteed coupling.
- **Extend bypass to `apikey` mode** if the wizard also fires there.
- **`ANTHROPIC_API_KEY` guardrail** — `/etc/profile.d/claude-auth.sh`
  appends `unset ANTHROPIC_API_KEY` in oauth mode so stray exports can't
  silently switch billing.
- **Version-diff skip** — compare installed version to stored
  `lastOnboardingVersion`; skip rewrite if equal. Minor speedup.
- **Boot-time `/status` audit** — log auth method on every boot so fleet
  operators spot drift without SSHing in.

## Definition of done

1. `claude` interactive on the VM enters a session without the login wizard.
2. `/status` confirms subscription-based auth via `CLAUDE_CODE_OAUTH_TOKEN`.
3. `ANTHROPIC_API_KEY` is unset in the shell that runs `claude`.
4. Rebuilding the image and recreating the workstation reproduces steps 1-3
   without manual intervention.

## Open questions (resolved)

- **Execution user** — `entrypoint.sh` runs as root, `claude` runs as
  `user`. Write goes to `/home/user/.claude.json` via `sudo -u user`.
- **Location** — lives in `entrypoint.sh` (runs every boot) so the
  workstation is usable immediately on first login.
