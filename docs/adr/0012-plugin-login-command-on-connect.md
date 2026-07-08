# ADR 0012: plugin `login-command:` run by `ra connect`

- **Status:** Superseded by [0014](0014-connection-config-and-plugin-commands.md)
- **Date:** 2026-07-08
- **Deciders:** @adamtait

> **Superseded.** The connect command moved from the plugin manifest to
> client-side config (`connect.login_command`), and the Eternal Terminal
> transport moved out of core into the `et` plugin via a plugin-command
> framework. See ADR 0014. This document is kept for history.

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
login-command: "tmux new-session -A -s main"
```

`login-command:` is run **verbatim** — there is no config-value interpolation in
this ADR. The command therefore hard-codes its own literals (the tmux plugin uses
the session name `main`, matching that plugin's `session_name` default). Making the
command reference plugin config generically (so a customized `session_name` flows
through) is a real want, but it is a broader, generic-templating decision deferred
to **[ADR 0013](0013-generic-plugin-value-templating.md)** and its tracking issue —
not something to bolt onto this field piecemeal. The only load-time check is that
the command is a single line.

### Why a verbatim command is shell-agnostic

`gcloud/ssh` and `et` run the remote command via the login shell's `-c`. A command
that is a plain argv — `tmux new-session -A -s main` — is parsed identically by
every shell's `-c`; the shell-specific divergences (`${VAR:-x}`, `||`, brace
expansion, fish's non-POSIX operators) only bite when the *command string itself*
uses them, which is the plugin author's choice. Because the command is authored in
`plugin.yaml` (trusted, like any plugin script) and no user-config value is
interpolated, there is no injection surface and nothing to validate beyond
"single line". (Value validation *would* be needed once templating lands — that is
part of what ADR 0013 must specify.)

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
- Adds a `--command` + forced-PTY invocation to the SSH path. Single `-t`
  **verified** on a live workstation: `gcloud workstations ssh <ws> --command …
  --ssh-flag=-t` under an interactive local tty allocates a real remote PTY
  (`tty` → `/dev/pts/0`); `-tt` is not needed. (Const `sshForcePTYFlag` if it ever
  is.)
- **A login-command that drives a terminal program must carry its own `TERM`
  fallback.** It runs *before* any shell, so the per-shell hooks' `infocmp -- "$TERM"
  || export TERM=xterm-256color` guard has no equivalent — a bare
  `tmux new-session -A` fails to attach ("missing or unsuitable terminal") when the
  client's `$TERM` (e.g. `xterm-ghostty`/`xterm-kitty`) has no terminfo on the
  workstation. **Reproduced live** on workstation `op` (missing ghostty/kitty terminfo
  even with `ncurses-term` installed): over a real PTY, a `TERM=xterm-256color` attach
  succeeded (session created) while the propagated `TERM=xterm-ghostty` attach exited
  at once with `missing or unsuitable terminal: xterm-ghostty`. The fix belongs in the
  *plugin's* login-command,
  and stays shell-agnostic by wrapping in `sh -c` (the login shell just execs `sh`):

  ```yaml
  login-command: "sh -c 'infocmp -- \"$TERM\" >/dev/null 2>&1 || export TERM=xterm-256color; exec tmux new-session -A -s main'"
  ```

  The whole string is plugin-authored and trusted, and runs verbatim; the `sh -c`
  wrapper keeps it shell-agnostic because the login shell only has to pass a
  single-quoted literal to `sh`.

### Neutral

- `login-command:` lives at the top level of `plugin.yaml`, beside
  `port-for-tunnel:`/`stable_id:`. If per-plugin connect behavior grows (env,
  working dir, fallback policy), it may move under a `connect:` block.

## Verification

1. `go build ./...` and `go test ./...` pass.
2. `Registry.LoginCommand` returns none / the single (verbatim) command / an error
   on multiple, and skips disabled plugins; a multi-line `login-command` is rejected
   at load. (`internal/plugins/login_command_test.go`.)
3. `runConnect` with a login-command emits `--command <cmd>` and
   `--ssh-flag=-t`; with none, the SSH argv is unchanged.
   (`cmd/connect_test.go`.) The `--et` path appends the command after the host.
   (`internal/etconn/args_test.go`.)
4. **Live (workstation `op`):** `--command … --ssh-flag=-t` under an interactive
   tty allocates a remote PTY (`/dev/pts/0`); without `-t` there is none. Over that
   PTY, `tmux new-session -A` with a valid `TERM` **attaches** and creates the
   session; with the propagated `xterm-ghostty` (no terminfo on `op`) it exits with
   `missing or unsuitable terminal` — motivating the `TERM`-fallback wrapper above.
   Full `ra connect --et` end-to-end and a real multi-shell run remain a post-merge
   check once the ra-plugins `login-command` ships.
