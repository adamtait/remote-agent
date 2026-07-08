# ADR 0012: plugin `login-command:` run by `ra connect`

- **Status:** Accepted
- **Date:** 2026-07-08
- **Deciders:** @adamtait

## Context

A plugin like `tmux` wants `ra connect` to land the user directly in a
persistent session rather than a bare shell. Today that is done with
**per-shell login hooks** baked into the workstation image — a bash
`/etc/profile.d/05-tmux-autoattach.sh` and a zsh `/etc/zsh/zlogin` snippet — each
of which runs `tmux new-session -A` when a login shell of that flavor starts.

That approach does not generalize. Each supported shell needs its own hook, in
its own syntax, installed in its own location, with its own delivery quirks (zsh
uses a `zsh-common` dpkg conffile installed at boot; fish would want
`vendor_conf.d`; nushell has no system drop-in at all). Adding a shell means
adding a hook. The login shell is also chosen by the user's dotfiles, so the
plugin is chasing a moving target.

The robust fix is to attach at the **connection layer**: have `ra connect` run
`tmux new-session -A -s <name>` as the *remote command* in place of the login
shell. Because the command replaces the shell, it behaves identically under any
login shell — bash, zsh, fish, nu — and there is a single place to get it right.

### Constraints

- `ra` ships **no built-in plugins** and core **hard-codes no plugin name**
  ([CLAUDE.md][1], ADRs 0007/0008/0009). Any tmux-specific behavior must live in
  the plugin; core may only act on a generic, declared capability.
- `ra` shells out to `gcloud`/`et` for all remote interaction; the connect
  command must keep that contract.
- Plugin schema changes propagate through every installed plugin, so a new field
  must be optional with a clear zero-value meaning.

[1]: ../../CLAUDE.md

## Decision

Add one optional top-level `login-command:` string to the plugin schema. When an
**enabled** plugin declares one, `ra connect` (both the plain-SSH and `--et`
transports) runs it on the workstation in place of the login shell.

This is the generic-manifest-field pattern already established by
`port-for-tunnel:` (ADR 0001) — which explicitly anticipated "a second command
type" justifying such a field — and by `stable_id:` / `buckets:`. Core reads the
field for *whichever* single enabled plugin declares it; it never names a plugin.

### Plugin schema

```yaml
# plugin.yaml (tmux plugin, in the ra-plugins repo)
name: tmux
login-command: "tmux new-session -A -s {session_name}"
config:
  - name: session_name
    type: string
    default: main
```

`login-command:` supports `{config-field}` placeholders, resolved against that
plugin's config values with the schema default as fallback — the same
`{placeholder}` convention as `BucketSpec.NameTemplate` (`ResolveBucketName`).
`ra connect` renders `{session_name}` → `main`, yielding
`tmux new-session -A -s main`.

### Why render a literal command (the shell-agnostic crux)

Rendering client-side to a **metacharacter-free argv** is what makes this
shell-agnostic. `gcloud/ssh` and `et` run the remote command via the login
shell's `-c`. A command containing `${VAR:-x}` or `||` would be parsed
differently by bash/zsh vs fish/nu. `tmux new-session -A -s main` is just a
command with arguments, so every shell's `-c` runs it identically. The session
name therefore comes from config at render time, not from a remote `${…}`
expansion.

To keep that invariant true rather than merely intended, each substituted value
is validated at render time against `^[A-Za-z0-9._:@/+=-]+$` (non-empty, no
whitespace, no shell metacharacters). A malformed config value — e.g.
`session_name: "a; rm -rf /"` or `"two words"` — fails `ra connect` with a clear
error instead of being baked into a command that only *looks* safe. This is a
robustness guard within the user's own trust domain (a plugin can already run
arbitrary code via `value_command`/boot scripts), not a privilege boundary. The
template itself is authored by the plugin and trusted; only the interpolated
config values are validated.

### CLI plumbing

- Plain SSH: `Client.SSH` gains a `command` argument. When non-empty it appends
  `--command <cmd>` and forces a PTY with `--ssh-flag=-t` (a remote command
  otherwise suppresses PTY allocation, leaving tmux without a terminal); empty
  reproduces today's interactive-shell argv exactly.
- `--et`: `ETOptions.Command` is appended after the `user@host` token; `et`
  forwards it to the remote shell. `et` already drives a raw-mode PTY, so no TTY
  flag is needed.
- `cmd/connect.go` resolves the command once via `Registry.LoginCommand(cfg)` and
  threads it through; `runConnect`/`runConnectET` take it as a parameter (kept
  testable — the registry lookup is unit-tested separately).

### More than one declaring plugin

`Registry.LoginCommand` returns an error naming the conflicting plugins if two
enabled plugins declare a `login-command:`. Two plugins cannot both own the login
shell; core refuses to silently pick a winner.

### Relationship to the per-shell login hooks

The per-shell hooks are **kept**, not removed. They still serve entry points that
don't go through `ra` — chiefly the code-oss browser IDE terminal and a raw
`gcloud workstations ssh`. There is **no double-attach**: the remote command runs
via `$SHELL -c` (non-login/non-interactive, so profile.d/zlogin don't fire), and
inside tmux the panes have `$TMUX` set so the hooks no-op. They coexist by entry
point; retiring the hooks is a separate, later decision.

## Alternatives Considered

1. **Hard-code tmux in `ra connect`.** Rejected — violates the plugin-agnostic
   core principle that ADRs 0007–0009 exist to uphold.
2. **A general plugin-command framework** (`ra <plugin> <verb>`). Rejected as
   premature, same as in ADR 0001. One generic field is trivially small; a
   framework can absorb it later if a second need appears.
3. **Server-side sshd `ForceCommand`.** Rejected — must special-case
   scp/rsync/non-interactive traffic; fragile, and the per-shell hooks already
   beat it on robustness.
4. **Keep adding per-shell hooks.** The status quo; rejected as the thing this
   ADR replaces — it is O(shells) work with per-shell delivery hazards.

## Consequences

### Positive

- One place, shell-agnostic: `ra connect`/`--et` land in the session under any
  login shell, with no per-shell image hooks needed for the `ra` entry points.
- Plugin authors get a one-line, name-agnostic opt-in; existing plugins are
  unaffected (optional field, empty zero value).
- Single source of truth: the session name comes from the same
  `plugins.<name>.session_name` config the plugin already reads.

### Negative

- No fallback in v1: if the rendered program (tmux) exits non-zero, the
  connection closes rather than dropping to a shell. A fallback would reintroduce
  shell-specific syntax, defeating the point; revisit if it bites.
- Adds a `--command` + forced-PTY invocation to the SSH path. The single-`-t`
  choice was **not verified against a live workstation** (none existed when this
  landed); `-t` is the standard-correct choice for an interactive local tty, with
  `-tt` a one-line fallback if a live test shows gcloud's wrapper needs it.

### Neutral

- `login-command:` lives at the top level of `plugin.yaml`, beside
  `port-for-tunnel:`/`stable_id:`. If per-plugin connect behavior grows (env,
  working dir, fallback policy), it may move under a `connect:` block.

## Verification

1. `go build ./...` and `go test ./...` pass.
2. `Registry.LoginCommand` returns none / the single rendered command / an error
   on multiple; `RenderLoginCommand` uses the config value and falls back to the
   schema default. (`internal/plugins/login_command_test.go`.)
3. `runConnect` with a login-command emits `--command <rendered>` and
   `--ssh-flag=-t`; with none, the SSH argv is unchanged.
   (`cmd/connect_test.go`.) The `--et` path appends the command after the host.
   (`internal/etconn/args_test.go`.)
4. **Live (post-merge, needs a workstation + the ra-plugins `login-command`):**
   `ra connect` and `ra connect --et` land directly in the `main` tmux session
   from both a bash and a zsh login shell — no dependence on the login shell.
