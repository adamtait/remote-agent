# Implementation notes — plugin `login-command` on connect

> Working notes for the `login-command` feature (PR #144) + its ra-plugins consumer
> (adamtait/ra-plugins#17). Sections below are chronological — later sections
> supersede earlier ones where they conflict (notably: **templating was removed** —
> see "Templating removed"). Moved into the repo at the user's request; if the
> `implementation-notes-*.md` housekeeping cleanup lands, reconcile accordingly.

Feature: `ra connect` runs a plugin-declared command (e.g. `tmux new-session -A -s main`)
as the remote command, shell-agnostically, instead of relying on per-shell login hooks.

## Blocker: no live workstation for the PTY probe (later resolved on `op`)
The plan's #1 verification (does `gcloud workstations ssh --command … --ssh-flag=-t`
allocate an interactive PTY?) could not run initially: workstation `p` was deleted
(`ra list` → none; gcloud → 404). Implemented with a single `-t`
(const `sshForcePTYFlag = "--ssh-flag=-t"`); `ssh -t host <cmd>` forcing a PTY is
standard and `ra connect` has a real local tty, so `-t` (not `-tt`) is correct.
**Later verified live on `op`** — see "Live verification".

## Design decisions (not fully spelled out in the plan)
- **Registry lookup vs connect flow:** the cobra `RunE` resolves the command once via the
  package-global `reg` (`reg.LoginCommand(cfg)`) and passes the resolved **string** into
  `runConnect`/`runConnectET` (new `loginCmd` param). Keeps the connect flow unit-testable by
  passing a literal command; isolates the registry lookup (tested in the plugins package).
- **`client.SSH` gains a `command` param** (not a new method). Non-empty ⇒ appends `--command`
  + the PTY flag; empty ⇒ byte-for-byte today's argv. One caller.
- **>1 enabled plugin declaring one ⇒ hard error** (two plugins can't both own the shell).
- **tmux-failure fallback: none in v1** (a fallback needs shell-specific syntax, defeating
  shell-agnosticism). Documented in the ADR.
- **ADR is 0012** — `0011-eternal-terminal-connect.md` already exists.
- **[SUPERSEDED] Rendering/templating:** the first cut interpolated `{config-field}`
  placeholders (like `BucketSpec.NameTemplate`) with per-value shell-safety validation.
  This was **removed** — see "Templating removed".

## Review round (independent code-reviewer agent) → fixes
- **Important (fixed at the time):** the metacharacter-free invariant was enforced only on the
  *template*, not the substituted *values*; `RenderLoginCommand` was made to validate each value
  against a shell-safe allowlist. *(Moot now that templating is removed — the command is a
  trusted verbatim string.)*
- Reviewer confirmed no critical/security issues; `--ssh-flag=-t` token form, enabled-gate
  nil-map semantics, empty-command passthrough, and et arg order all verified correct.

## Live verification on workstation `op`
- **`--command … --ssh-flag=-t` allocates a real remote PTY** (`tty` → `/dev/pts/0`) under an
  interactive local tty; without `-t`, "not a tty". **Single `-t` is correct — `-tt` NOT
  needed.** (Simulated the tty via a Python `pty.fork` runner; BSD `script` was flaky —
  `tcgetattr/ioctl not supported on socket`.)
- **Both directions reproduced end-to-end over a real PTY**, with a remote `timeout` so nothing
  hangs: valid `TERM=xterm-256color` → tmux attaches, session created; propagated
  `TERM=xterm-ghostty` → `missing or unsuitable terminal: xterm-ghostty`, no session.
- **Terminfo gap is genuine:** `op` has `ncurses-term` installed but still lacks
  `xterm-ghostty`/`xterm-kitty` (that ncurses 6.4 predates them). → the plugin's login-command
  carries a `sh -c` TERM fallback (`infocmp … || export TERM=xterm-256color; exec tmux …`),
  which stays shell-agnostic (the login shell just execs `sh`).

## Templating removed (user decision)
Per user: config-value templating, if added, should be **generic** — not bolted onto
`login-command` alone (which would repeat the bespoke `BucketSpec.NameTemplate` one-off).
So templating was **removed from these PRs**:
- remote-agent: dropped `RenderLoginCommand`, `loginValueString`, `loginValueRe`,
  `loginCommandPlaceholderRe`, and the placeholder validation in `LoadPlugin` (kept only the
  single-line check). `Registry.LoginCommand` now returns the plugin's `login-command`
  **verbatim**. Security story simplifies: no user-config value is interpolated, so there's no
  injection surface and nothing to validate beyond "single line".
- ra-plugins tmux/plugin.yaml: `{session_name}` → literal `main` (matches `session_name`'s
  default). **Known limitation:** if a user overrides `session_name`, the boot session is named
  that but the login-command still attaches to `main` (would create a second session). That
  mismatch is exactly what generic templating would fix.
- Captured as future work: **ADR 0013** (`docs/adr/0013-generic-plugin-value-templating.md`,
  status Proposed) + **issue #147**. ADR 0012, CLAUDE.md, PR/ADR wording updated to "verbatim".

## Current file inventory (branch `feat/plugin-login-command`)
- `internal/plugins/schema.go` — `LoginCommand` field (verbatim, no templating)
- `internal/plugins/fileplugin.go` — single-line validation only
- `internal/plugins/registry.go` — `Registry.LoginCommand` (verbatim, single/none/error-on->1)
- `internal/gcloud/workstation.go` — `SSH` `command` param + `sshForcePTYFlag`
- `internal/etconn/args.go` — `ETOptions.Command`, appended after host
- `cmd/connect.go` — resolve in `RunE`; thread `loginCmd`
- tests: `internal/plugins/login_command_test.go`, `internal/etconn/args_test.go`,
  `cmd/connect_test.go`
- `docs/adr/0012-…` (feature) + `docs/adr/0013-…` (deferred templating) + `CLAUDE.md`

## Repo state I did NOT create (still in the working tree)
At branch time the tree already had an unrelated housekeeping changeset (~819 deletions):
deleted `implementation-notes-{et,teardown,}.md` + `docs/headless-claude-auth-plan.md`, an ADR
rename, `README.md`/`go.mod`/`go.sum`/`docs/adr/README.md` edits, new `.github/workflows/`.
**Not mine.** Every commit here is scoped to explicit feature files — never `git add -A`.
