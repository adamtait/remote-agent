# ADR 0013: generic templating for plugin manifest values

- **Status:** Withdrawn (see [0014](0014-connection-config-and-plugin-commands.md))
- **Date:** 2026-07-08
- **Deciders:** @adamtait
- **Tracking:** [#147](https://github.com/adamtait/remote-agent/issues/147)

> **Withdrawn.** This templating was motivated by interpolating config values
> into the plugin `login-command:`. ADR 0014 moved the connect command to
> user-authored config and made plugin commands run verbatim scripts, so there
> is nothing to interpolate. Revisit generically (issue #147) only if a concrete
> need reappears.

## Context

Multiple `plugin.yaml` string fields want to interpolate values — the plugin's own
config, or global facts like the gcloud project — but there is no shared mechanism:

- **`BucketSpec.NameTemplate`** (ADR 0007-era) does a bespoke `strings.ReplaceAll` of
  `{project_id}` / `{user_email_hash}` in `internal/gcs/ResolveBucketName`.
- **`login-command:`** (ADR 0012) deliberately ships **without** templating: it runs
  verbatim, so the tmux plugin hard-codes the session name `main`. A user who sets
  `plugins.tmux.session_name` to something else gets a mismatch — the boot session is
  named that, but the login-command still attaches to `main`.

Each new field re-implements substitution (or forgoes it). We should decide the
generic mechanism once, rather than growing `login-command`-specific templating that
would just become the next `BucketSpec` one-off.

## Decision (proposed — not yet implemented)

Introduce **one** templating helper in `internal/plugins`, used by every manifest
field that interpolates, with **per-sink value validation** supplied by the caller.
`login-command` and (ideally) `BucketSpec.NameTemplate` migrate onto it.

Sketch:

- **Syntax:** keep the existing `{name}` brace convention (not Go `text/template`) —
  it's already precedented by `BucketSpec` and is trivially readable.
- **Resolvable names:** the plugin's own config fields (configured value, else schema
  default), plus the existing global tokens (`{project_id}`, `{user_email_hash}`).
- **Per-sink validation is mandatory and lives with the sink.** Interpolating an
  untrusted config value into a shell command vs a GCS bucket name needs different
  safety rules. For `login-command` the value must be shell-safe (the
  `^[A-Za-z0-9._:@/+=-]+$`-style allowlist prototyped in #144's earlier revision —
  the template stays plugin-authored/trusted, only interpolated values are checked).
  The templating core takes a validator callback; it does not hard-code one policy.
- **Non-scalar fields:** a list-valued field is an error for scalar sinks like
  `login-command`.

## Alternatives

1. **Per-field bespoke substitution** (status quo: `BucketSpec`, and what a naive
   `login-command` templating would add). Rejected as the thing this ADR removes.
2. **Go `text/template`.** More powerful (conditionals, functions) than the need;
   introduces a second templating dialect alongside the `{name}` one already in the
   manifest. Deferred unless a real case needs logic.
3. **No templating ever; hard-code literals.** What ADR 0012 ships as an interim. Fine
   for a single default, but breaks the single-source-of-truth the moment a user
   customizes the underlying config value.

## Consequences

- **Positive:** one place to get interpolation + escaping right; `login-command` can
  reference `{session_name}` so it can't drift from the boot session; `BucketSpec`
  folds in.
- **Negative:** a real API surface to design (validator injection, error paths, list
  handling) — hence Proposed, not bundled into #144/#17.
- **Neutral:** until accepted, `login-command` stays verbatim (ADR 0012).

## Verification (when implemented)

1. A shared resolver renders `{field}` from config with schema-default fallback, and
   the global tokens, with unit tests.
2. `login-command` templating rejects a shell-unsafe interpolated value (e.g.
   `session_name: "a; rm -rf /"`) with a clear error; `BucketSpec` still enforces
   GCS-name rules.
3. `ra connect` attaches to the *configured* session name, not a hard-coded literal.
