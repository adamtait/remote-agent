# Implementation notes — connection config + plugin-defined commands

> Re-design (supersedes the login-command-as-plugin-field work). Running log of
> decisions/tradeoffs not fully pinned by the plan
> (`~/.claude/plans/do-1-and-file-wise-abelson.md`). Replaces the old
> `implementation-notes-login-command.md`.

## Requirements
1. `connect.login_command` lives in **config** with a plain-shell default.
2. Remove `--et`/`runConnectET`/`internal/etconn` from core.
3. Plugin-defined commands → `ra plugin <name> <command>`; the `et` plugin uses
   this to own ET connection. Plus a `post_install_message` install hook.

## Decisions made during implementation (not spelled out in the plan)

### Registration site: root.go `init()`, not `Execute()`
The plan said call `registerPluginCommands` from `Execute()`. I put it at the end
of `cmd/root.go`'s `init()` instead, because: (a) Go runs package `init()`s in
lexical file order → `plugin.go` (creates `pluginCmd`) runs before `root.go`, so
both `pluginCmd` and a loaded plugin set exist; (b) `init()` runs exactly once, so
`ExecuteWithIO` (called repeatedly in tests) won't double-register. `registerPluginCommands`
takes an explicit `([]*FilePlugin, *cobra.Command)` so it's unit-testable.

### Project-level plugin discovery at registration
The existing early load in `init()` was user-dir-only. To register project-level
plugin commands too, registration replicates the project-dir discovery
(`config.FindProjectConfig(wd, home)` → `<dir>/plugins`) — same derivation
`PersistentPreRunE` uses. Best-effort: any error → user-dir only.

### `FilePlugin.Dir()`
`LoadPlugin` only gets an `fs.FS`, not a path, so it can't know its on-disk root.
Added an unexported `dir` field set in `loader.go`'s `loadDir`
(`filepath.Join(dir, e.Name())`) + a `Dir()` accessor. Plugins loaded via a bare
`LoadPlugin(os.DirFS(...))` (some tests) have an empty `Dir()`; only the
LoadAll/loadDir path (used for command registration) needs it.

### Leaf command RunE reads globals late
Plugin-command `RunE` closures capture `(pluginDir, CommandSpec)` at registration
time but read `cfg`/`client` (globals) only when run — they're populated by
`PersistentPreRunE`, which cobra runs before any `RunE`. The `login_command` passed
to the script is `cfg.Connect.LoginCommand`.

### Env contract for plugin command scripts
`RA_WORKSTATION, RA_PROJECT, RA_REGION, RA_CLUSTER, RA_CONFIG, RA_WORKSTATION_USER,
RA_LOGIN_COMMAND`, plus the dir of the running `ra` binary prepended to `PATH` so
scripts can call `ra tunnel`. Set only after resolve when `needs_workstation: true`.

### ET removal is a clean delete
`internal/etconn/` deleted wholesale; et bits removed from `cmd/connect.go` and
`internal/gcloud/workstation.go` (`ETServerReady`/`etReadySentinel`).
`StartTCPTunnel(+Context)`/`SSHReady` kept (generic/reused). `connectCmd.RunE` now
reads `cfg.Connect.LoginCommand` (no more `reg.LoginCommand`).

### Docs / ADRs
New ADR 0014 (this design); ADR 0012 → Superseded, 0013 → Withdrawn (login-command
is no longer a plugin field, so per-value templating is moot for it).

### `StartTCPTunnelContext` kept though now unused in core
`internal/etconn` was its only caller. Kept it (plan says keep both tunnel
primitives) — it's a generic cancellable-tunnel helper a future feature could use,
and Go doesn't flag unused exported methods. Keeps the `context` import live.

### Plugin-command exit code flattened to 1
`runPluginCommand` returns `errSilent` on script failure, so ra exits 1 without
printing over the script's own output — but it does NOT propagate the script's
exact exit code (ra's top-level `Execute` always `os.Exit(1)` on error).
Acceptable for v1; noted in ADR 0014.

### et plugin script: supervised tunnels + teardown
`et/commands/connect.sh` uses `set -m` (job control) so the EXIT trap's
`kill %1 %2` reaches each `ra tunnel` job's whole process group (incl. the gcloud
child). et runs in the FOREGROUND (not `exec`) precisely so the trap still fires
when et returns — an `exec` would replace the script and drop the trap, leaking the
tunnels. etserver readiness is polled via a `gcloud ... ssh --command` /proc/net/tcp
probe (the old core `ETServerReady`, now living in the plugin).

## Final state — all green
`go build/vet ./...`, `gofmt -l`, `go test ./...` clean; `ra connect --et` →
`unknown flag`. ra-plugins `et`/`tmux` plugin.yaml validated against the real core
(`LoadPlugin` + `registerPluginCommands` register `ra plugin et connect`; tmux
`post_install_message` set); `bash -n` + `shellcheck` clean on `connect.sh`.

Not runtime-verified end-to-end (needs image rebuild with the new plugins + a live
workstation): the actual `ra plugin et connect` session. Planned on `op`.

## Review round (independent code-reviewer) → fixes
- **Important (real bug, fixed):** `connect.sh` passed `RA_LOGIN_COMMAND` as a
  trailing positional to `et` — but `et` takes a remote command via `-c/--command`
  (verified via `et --help`: only `[user@]host[:port]` is positional). A trailing
  positional would be dropped or error. This was inherited from PR #144's
  never-live-verified `buildETArgs` trailing-token. Fixed: `args+=(--command "$RA_LOGIN_COMMAND")`.
- **Important (fixed):** `runPluginCommand` mapped *any* `sub.Run()` error to
  `errSilent` — including failed-to-start (EACCES/ENOEXEC), which would exit 1 with
  a blank screen. Now: `errors.As(&exec.ExitError)` ⇒ script ran, silence; otherwise
  return a real error.
- **Suggestion (applied):** `supervise()` could busy-loop if `ra tunnel` fast-fails
  (e.g. `ra` not on PATH). Added capped backoff (1→2→…→30s on fast-fail, reset to 1s
  after a real drop).
- **Noted, no change:** `RA_WORKSTATION_USER` hardcoded "user" (correct for GCP
  Workstations; `ref` has no user field to wire). Reviewer confirmed init ordering,
  `PersistentPreRunE` population of cfg/client, process-group teardown (`set -m` +
  `kill %1 %2`), the /proc/net/tcp probe, config merge precedence, and no
  shell-injection — all sound.
