# ADR 0014: connection config + plugin-defined commands

- **Status:** Accepted
- **Date:** 2026-07-08
- **Deciders:** @adamtait
- **Supersedes:** two short-lived drafts, both removed and folded in here — a
  plugin-field `login-command:` and a generic value-templating proposal (their
  still-relevant findings are under Alternatives and Consequences).

## Context

Two earlier (unshipped) approaches needed revising:

- An earlier draft put the connect-time command on the *plugin*
  (`plugin.yaml login-command:`, resolved by `Registry.LoginCommand`). But the
  connect command is a *user* preference, not a plugin trait — a singleton the
  user should own and change.
- The **Eternal Terminal transport** (`--et`, `runConnectET`, `internal/etconn`)
  was baked into `ra` core, contradicting "core ships no plugin-specific
  behavior." ET is one connection style among potentially many; it belongs in a
  plugin.

Making ET a plugin requires a way for a plugin to contribute a *command* that
runs locally (open tunnels, run the local `et` client) — which `ra` had no
mechanism for. ADR 0001 anticipated this ("a general plugin command framework …
if/when a second command type proves necessary").

## Decision

Three small, generic additions; remove the ET code and the plugin `login-command`
field.

### A. `connect.login_command` — connection config (client-side)
A new typed `connect:` block in config (`internal/config` `ConnectConfig`):
```yaml
connect:
  login_command: "sh -c '… exec tmux new-session -A -s main'"   # optional
```
`ra connect` runs it in place of the login shell (`gcloud … ssh --command … -t`).
Unset (the default) opens a plain interactive login shell. Purely client-side —
merged project-over-user like other scalars, read in `cmd/connect.go`, never
propagated to the workstation. `ra setup` scaffolds it commented, with a tmux
example. This replaces `Registry.LoginCommand`.

### B. Plugin-defined commands → `ra plugin <name> <command>`
`plugin.yaml` gains a `commands:` list (schema-level, like `buckets:`):
```yaml
commands:
  - name: connect
    description: "Connect via Eternal Terminal (auto-reconnecting)"
    run: commands/connect.sh    # local script, path relative to the plugin dir
    needs_workstation: true
```
`ra` registers each as `ra plugin <plugin-name> <command-name>` under the existing
`plugin` group (namespaced — arbitrary names, no top-level collisions). On invoke
it (optionally) resolves + ensures the workstation running, then **execs the
plugin's local script** with stdio wired to the terminal and an env contract:
`RA_WORKSTATION, RA_PROJECT, RA_REGION, RA_CLUSTER, RA_CONFIG,
RA_WORKSTATION_USER, RA_LOGIN_COMMAND` (= `connect.login_command`), plus the `ra`
binary on `PATH` so scripts can call back into `ra` (e.g. `ra tunnel`).

Registration happens at process start (a package `init()`), before
`rootCmd.Execute()` — cobra selects the target command from the already-built
tree, so a command registered later would be "unknown". It discovers user- and
project-level plugins (project via `config.FindProjectConfig` walk-up). Core stays
plugin-agnostic: no plugin name is hard-coded; a plugin whose name shadows a
built-in `plugin` subcommand (`install`/`list`/`remove`) is skipped with a warning.

### C. `post_install_message` — install hint
An optional `plugin.yaml` string printed by `ra plugin install` on success.
Lets a plugin recommend a config change it shouldn't make itself (e.g. tmux
recommending a `connect.login_command`). Not auto-applied.

### D. ET removed from core; provided by the `et` plugin via (B)
`internal/etconn`, `--et`, `runConnectET`, and `gcloud.ETServerReady` are deleted.
The `et` plugin ships `commands/connect.sh` (registered as `ra plugin et connect`)
that opens the bootstrap+data tunnels (via `ra tunnel` in shell respawn loops,
preserving supervision) and runs the local `et` client, honoring
`RA_LOGIN_COMMAND`. `gcloud.StartTCPTunnel(+Context)` and `SSHReady` stay (generic).

## Alternatives considered

- **Top-level `ra <name>` for plugin commands.** Rejected: collides with core /
  future commands and needs a reserved-name policy. `ra plugin <name> <command>`
  namespaces cleanly and reads as "a command from this plugin."
- **Auto-seed `connect.login_command` at `ra plugin install`.** Rejected: a
  per-plugin install mutating a singleton top-level setting is semantically
  awkward (last install wins). A printed recommendation (C) is enough.
- **Ship ET as a built-in flag but move orchestration to a library.** Rejected:
  still core-resident plugin-specific behavior. (B) removes it entirely.
- **Server-side `sshd ForceCommand`** (instead of a client-side command). Rejected:
  it would have to special-case scp/rsync/non-interactive traffic, and is more
  fragile than the client passing `--command`. Client-side `connect.login_command`
  + plugin commands keep the mechanism visible and per-invocation.
- **Config-value templating for `login_command`** (a withdrawn draft ADR). The idea:
  one `internal/plugins` templating helper interpolating `{config-field}` (and
  global tokens `{project_id}`/`{user_email_hash}`) across manifest fields, so
  `login_command` couldn't drift from the boot session's `session_name` and the
  bespoke `BucketSpec.NameTemplate` substitution could fold in. Withdrawn because,
  with the connect command now user-authored config and plugin commands running
  verbatim scripts, **nothing is interpolated** — there is no drift to prevent. If
  revived (tracked in [#147](https://github.com/adamtait/remote-agent/issues/147)),
  the shape worth keeping: reuse the `{name}` brace convention (not Go
  `text/template` — avoid a second dialect); make **per-sink value validation**
  mandatory and caller-supplied (a shell sink needs a shell-safe allowlist like
  `^[A-Za-z0-9._:@/+=-]+$` — template stays trusted, only interpolated values are
  checked; a GCS-name sink differs); reject non-scalar (list) values for scalar
  sinks. Rejected sub-option: **per-field bespoke substitution** (the `BucketSpec`
  status quo) — it just multiplies one-offs.

## Consequences

### Positive
- `ra connect` behavior is a user setting with an obvious default; core carries no
  ET code and no per-plugin connect logic.
- Plugins extend the CLI generically; ET is just the first user of the framework.
- The env contract makes `RA_LOGIN_COMMAND` transport-agnostic — `ra plugin et
  connect` attaches the same session `ra connect` would.

### Negative
- ET orchestration moves into a plugin shell script (tunnel supervision via a
  `ra tunnel` respawn loop) — more shell, and the exact exit code of a plugin
  command is flattened to 1 by ra's top-level error path (v1).
- Project-level plugin **commands** register only when `ra` runs from within the
  project tree (cwd walk-up at init) — acceptable; runtime data still uses the
  authoritative registry from `PersistentPreRunE`.

### Neutral
- `connect:` and `commands:`/`post_install_message:` sit at the top level of their
  respective files, beside existing generic fields.

### Findings that shape usage (carried from the removed drafts)
- **PTY flag: single `-t`, not `-tt`.** `Client.SSH` appends `--command … --ssh-flag=-t`;
  a remote command otherwise suppresses PTY allocation. Verified live on workstation
  `op`: under an interactive local tty, `gcloud workstations ssh … --command …
  --ssh-flag=-t` allocates a real remote PTY (`tty` → `/dev/pts/0`); `-tt` was not
  needed. Kept as a `sshForcePTYFlag` const so it's a one-line change if a future
  wrapper ever needs `-tt`.
- **A `login_command` that drives a terminal program must carry its own `TERM`
  fallback**, because it runs *before* any shell — there is no profile.d hook to run
  `infocmp -- "$TERM" || export TERM=xterm-256color`. Reproduced live on `op` (which
  lacked `xterm-ghostty`/`xterm-kitty` terminfo even with `ncurses-term` installed): a
  bare `tmux new-session -A` under `TERM=xterm-ghostty` exits with
  `missing or unsuitable terminal`, while `TERM=xterm-256color` attaches. This is why
  the tmux plugin's recommended `connect.login_command` (and `ra plugin et connect`,
  via `RA_LOGIN_COMMAND`) wraps in `sh -c` with the guard:
  ```yaml
  connect:
    login_command: "sh -c 'infocmp -- \"$TERM\" >/dev/null 2>&1 || export TERM=xterm-256color; exec tmux new-session -A -s main'"
  ```
  The `sh -c` wrapper also keeps it shell-agnostic: whatever the login shell, it only
  has to pass a single-quoted literal to `sh`.
- **The image's per-shell tmux hooks stay** for non-`ra` entry points (the code-oss
  browser IDE terminal, a raw `gcloud workstations ssh`). No double-attach: the
  connect-time command runs via `$SHELL -c` (non-login), so profile.d/zlogin don't
  fire, and inside tmux `$TMUX` is set so those hooks no-op.

## Verification
1. `go build/vet ./...`, `gofmt -l`, `go test ./...` clean.
2. Config merge (project-over-user) + `ra connect` argv with/without
   `login_command` (`--command … --ssh-flag=-t` when set; unchanged when unset).
3. `LoadPlugin` validates `commands:` (name/run required, dedup);
   `registerPluginCommands` builds `ra plugin <name> <cmd>` and skips reserved
   names; the leaf execs its script with the env contract.
4. `ra plugin install` prints `post_install_message`.
5. `ra connect --et` → unknown flag; `grep -r etconn` clean.
6. Live on `op`: `ra plugin et connect` attaches via the local `et` client and
   honors `RA_LOGIN_COMMAND`; plain `ra connect` still works.
