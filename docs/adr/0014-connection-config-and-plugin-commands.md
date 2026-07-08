# ADR 0014: connection config + plugin-defined commands

- **Status:** Accepted
- **Date:** 2026-07-08
- **Deciders:** @adamtait
- **Supersedes:** [0012](0012-plugin-login-command-on-connect.md), [0013](0013-generic-plugin-value-templating.md)

## Context

Two earlier decisions need revising:

- **ADR 0012** put the connect-time command on the *plugin*
  (`plugin.yaml login-command:`), resolved by `Registry.LoginCommand`. But the
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
- **Config-value templating for `login_command`.** Was ADR 0013; withdrawn — with
  the command in config (authored by the user) and hard-coded literals in plugin
  scripts, there is nothing to interpolate. Revisit generically if a real need
  reappears.

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
