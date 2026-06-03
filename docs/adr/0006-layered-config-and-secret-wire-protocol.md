# ADR 0006: Layered config with a generic secrets/env passthrough and secret wire protocol

- **Status:** Accepted (retrospective record; the generic secrets/env model is current, the `claude`/`github` special-casing was **superseded** by [ADR 0009](0009-github-and-claude-as-external-plugins.md))
- **Date:** 2026-04-23
- **Deciders:** @adamtait

> **Retrospective note.** Backfilled from the now-removed
> `docs/superpowers/specs/2026-04-23-ws-layered-config-design.md` (git
> `334634a^`). Tool named `ws`; read as `ra`, and the `WS_`-prefixed env vars
> below are `RA_` today. The grouped schema and two-layer merge remain the
> foundation of `internal/config`; the hardcoded `claude_oauth` /
> `claude_apikey` / `github_pat` dispatch was generalized away as those concerns
> moved into external plugins ([ADR 0007](0007-file-based-plugin-system.md),
> [0008](0008-zero-builtin-plugin-image-mirror-architecture.md),
> [0009](0009-github-and-claude-as-external-plugins.md)).

## Context

The config merge from [ADR 0004](0004-go-cli-shelling-out-to-gcloud.md) had
grown ad hoc: a `defaults:`/`projects:` map, three-layer fallback, and
hardcoded Go defaults scattered through `Merge`. Two problems compounded it:

- **Silent defaults hide misconfiguration.** A missing setting silently took a
  Go-hardcoded value, so a user could provision the wrong thing without warning.
- **Credentials were hardcoded.** Only Claude auth and a GitHub PAT could reach
  the container, wired in by name. There was no way to forward an arbitrary
  secret or static env var to the workstation.

We wanted the user config to be the **exhaustive baseline** (every key present),
the project config a **thin override**, any missing required key to be a
**startup error**, and a **generic** mechanism to push secrets and env vars
into the container.

## Decision

### Grouped, two-layer config; no hardcoded defaults

Both files use grouped YAML (`git:`, `workstation:`, `project:`, `secrets:`,
`env:`). Precedence is exactly two sources — **project overrides user** (CLI
flags reserved as a future third layer). Every setting must come from one of
those files or `ra` fails at command start. The parsed structs are grouped;
`Merge` lifts them into a **flat** `Config` consumed by `cmd/*` so callers don't
churn.

### Validation before any GCP call

`internal/config/validate.go` runs in the root `PersistentPreRunE` (every
command except `setup`): required-key presence, exactly-one Claude auth method,
`env_var` names matching `^[A-Z_][A-Z0-9_]*$`, no collisions among
`secrets[*].env_var` / `env[*]` / reserved names, and asset-file presence.
`create` additionally checks each secret is reachable in GCP Secret Manager
before mutating anything.

### Generic secrets + env passthrough

`secrets:` maps a logical name to `{secret_name, env_var}`; `env:` is a plain
static passthrough. Both merge per-key across the two layers. This is the
zero-secrets design: config carries only the *mapping*, never the value — the
value lives in GCP Secret Manager and is fetched at boot.

### `ra create` → entrypoint wire protocol

`create` serializes the merged secrets into container env that the boot script
iterates:

```
WS_SECRETS=<pipe-separated logical names>   # pipe, not comma — gcloud --container-env splits on comma
WS_SECRET_<NAME>_NAME=<gcp-secret-name>
WS_SECRET_<NAME>_VAR=<target-env-var>
```

The boot script loops `$WS_SECRETS`, fetches each secret, and exports it under
its `env_var`. (Today these are `RA_`-prefixed; the pipe-not-comma constraint
persists — see CLAUDE.md on `string_list` pipe-joining.)

### Special-cased logical names (since removed)

At this stage three logical names got bespoke handling baked into core: a
`claude_oauth`/`claude_apikey` choice (one or the other) wiring Claude Code's
auth, and `github_pat` installing a git credential helper plus a `gh`-CLI
export. Everything else was a plain fetch-and-export.

## Alternatives considered

- **Keep hardcoded defaults.** Rejected: they mask misconfiguration; an explicit
  startup error is safer than a silently-wrong provision.
- **Backwards-compat migration of old configs.** Rejected per the project's
  no-backwards-compat rule: the first run against a stale config fails
  validation with "missing key …; run `ra setup`", and re-running setup
  regenerates a compliant file.
- **A separate logical secret for `oauth_account_secret_name`** rather than a
  sibling field on the `claude_oauth` entry. Deferred — locality won; revisitable.
- **Export `ANTHROPIC_API_KEY` at boot in apikey mode.** Deferred — apikey mode
  stayed helper-only, with `env_var` as documentation.

## Consequences

- Misconfiguration fails fast at command start with a named missing key, before
  any GCP mutation.
- Any secret or env var can be forwarded generically — the foundation the plugin
  system ([ADR 0007](0007-file-based-plugin-system.md)) built on, where each
  plugin declares its own `secrets:`.
- The grouped schema and flat post-merge `Config` are still how
  `internal/config` works today.
- **Superseded in part by [ADR 0009](0009-github-and-claude-as-external-plugins.md):**
  the special-cased `claude_*`/`github_pat` dispatch was deleted as Claude and
  GitHub became external plugins; core now treats *every* secret generically.
