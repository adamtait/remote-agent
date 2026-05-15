# ADR 0001: `ra tunnel` and the plugin `port-for-tunnel:` field

- **Status:** Accepted
- **Date:** 2026-05-15
- **Deciders:** @adamtait

## Context

Users routinely run network services *inside* a workstation — a marimo
notebook on 2718, a jupyter server on 8888, a dev HTTP server on 8080 — and
want to reach them from their laptop. Today the only path is invoking
`gcloud workstations start-tcp-tunnel` directly, which means spelling out
`--cluster`, `--config`, `--region`, `--project`, the workstation name, the
remote port, and the local port on every call. Plugins that *install and run*
those services (e.g. a hypothetical marimo plugin whose
`workstation-startup.d/300_marimo.sh` script runs `marimo edit --port 2718`)
have no way today to advertise "here's the port I listen on."

This ADR adds a first-class tunneling command and a minimal plugin hook so
the common case becomes `ra tunnel <plugin>`.

### Constraints

- `ra` shells out to `gcloud` for all GCP interactions ([CLAUDE.md][1]). The
  tunnel command must keep that contract.
- Plugin schema changes propagate through every installed plugin. Any new
  field must be optional and have a clear zero-value semantics.
- No new background process management. `ra` does not currently track
  long-lived processes; introducing PID files / `ra tunnel list/stop` is a
  meaningfully larger surface area than the use case demands.

[1]: ../../CLAUDE.md

## Decision

Add `ra tunnel <target> [workstation]` and a single optional
`port-for-tunnel:` field on the plugin schema. The field is also materialized
into the user-editable `plugins.<name>.port-for-tunnel` config block at
install time, so the same value drives both the server bind port (via env
var) and the tunnel target.

### CLI

```
ra tunnel <target> [workstation] [--local-port N] [--project ...] [--cluster ...] [--config ...]
```

The first positional argument can be one of three forms:

1. **Plugin name** (`ra tunnel marimo`) — remote port comes from the named
   plugin's `port:` field. Local port defaults to the same value;
   `--local-port` overrides.
2. **Single port** (`ra tunnel 8080`) — both local and remote port equal
   `8080`; `--local-port` overrides the local side.
3. **`local:remote` pair** (`ra tunnel 9000:8080`) — both sides explicit.
   `--local-port` is rejected in this form to avoid two sources of truth.

If `[workstation]` is omitted, the same resolution that `ra connect` uses
picks the target workstation (configured default → most recently created).
Stopped workstations are auto-started — same UX as `ra connect`. The command
runs in the foreground and exits when the underlying `gcloud
workstations start-tcp-tunnel` process exits (typically Ctrl-C).

### Plugin schema

A single new optional field on `PluginSchema`, at the top level alongside
`name`/`version`/`description`:

```yaml
# plugin.yaml
name: marimo
version: "1.0.0"
description: Marimo notebook UI
port-for-tunnel: 2718
```

`port-for-tunnel: 0` (the zero value, i.e. no `port-for-tunnel:` line)
means "this plugin opts out of named tunneling." `ra tunnel <name>`
resolves through the registry: if the named plugin is installed and has
`port-for-tunnel != 0`, it's the target; otherwise the command errors with
a list of plugins that *do* declare a tunnel port.

### Install-time rendering

`ra plugin install` automatically renders `plugins.<name>.port-for-tunnel:
<schema-default>` into `config.yaml` whenever the schema declares a non-zero
value, so the same value:

1. is exported into the workstation as
   `RA_PLUGIN_<NAME>_PORT_FOR_TUNNEL=<value>` and read by the plugin's
   `workstation-startup.d/` script to bind its server, AND
2. is read by `ra tunnel <name>` as the remote port, with the schema
   default as a fallback if the user has removed the key from
   `config.yaml`.

This single source of truth prevents the silent mismatch that would
otherwise occur if a user customized the bind port but the tunnel still
targeted the schema default.

### Implementation surface

- `internal/plugins/schema.go` — add `PortForTunnel int` to
  `PluginSchema` (yaml tag `port-for-tunnel`).
- `internal/plugins/fileplugin.go` — reject port outside `[0, 65535]` in
  `LoadPlugin`; `Prompt` injects `port-for-tunnel` into the rendered
  config map when the schema declares one;
  `containerEnvPairsFrom` emits `RA_PLUGIN_<NAME>_PORT_FOR_TUNNEL` reading
  from the config map with the schema default as fallback.
- `internal/plugins/registry.go` — `PortByName(name, cfg)` reads
  `cfg.Plugins[name]["port-for-tunnel"]` with schema fallback;
  `TunnelablePlugins() []string` returns a sorted name list.
- `internal/gcloud/workstation.go` — `Client.StartTCPTunnel(...)` wrapping
  `gcloud workstations start-tcp-tunnel`, mirroring `SSH`.
- `cmd/tunnel.go` — the cobra command and target-parsing logic.
- `cmd/resolve.go` — extract the describe→maybe-start block from
  `cmd/connect.go` into a shared `ensureRunning` helper that both `connect`
  and `tunnel` call.
- `plugins/marimo/` — first in-tree user: declares `port-for-tunnel: 2718`
  and reads `RA_PLUGIN_MARIMO_PORT_FOR_TUNNEL` in its startup script.

## Alternatives Considered

### 1. A general "plugin command" framework

Rejected for now. A declarative system where plugins register arbitrary
named subcommands (`ra <plugin> <command>` with command types like
`tcp_tunnel`, `open_url`, `exec`) would let plugins extend the CLI surface
freely. But the immediate need is *just* tunneling, and a single-purpose
`ra tunnel` keeps both the user-facing surface and the plugin contract
trivially small. If/when a second command type proves necessary, this ADR
can be superseded by the broader framework — `port-for-tunnel:` survives
the rewrite because it remains a useful piece of plugin metadata.

### 2. Backgrounded tunnels with `ra tunnel list/stop`

Rejected as out of scope. The foreground form ("run it, Ctrl-C when done,
put it in another terminal or shell-background it") matches the underlying
`gcloud workstations start-tcp-tunnel` behavior and adds no state for `ra`
to track. PID files, log redirection, and lifecycle management for
backgrounded tunnels are a meaningfully bigger commitment; we should see
real demand before taking it on.

### 3. Multiple ports per plugin (`tunnels:` list)

Rejected as premature. None of the foreseen plugins (marimo, jupyter,
single-purpose web UIs) need more than one declared port. A list-shaped
field is harder to roll back than a scalar one; we'll add `tunnels:` if a
plugin appears that genuinely needs it.

### 4. User-declared tunnels in `config.yaml`

Rejected as a redundant configuration surface. Users who want an ad-hoc
tunnel can already do `ra tunnel 8080` or `ra tunnel 9000:8080`. Named
tunnels belong with the thing that *runs* the service (the plugin), not in
a separate user file that has to be kept in sync.

### 5. Naming: `port:` vs `port-for-tunnel:`

An earlier draft used a top-level `port:` field. Rejected once a real
plugin (marimo) landed that already used `port:` as a *config-field name*
to let users customize the bind port. Two different `port:` meanings in
the same plugin would be confusing; `port-for-tunnel:` is more verbose but
unambiguous, and it leaves the conventional `config.port` name available
for plugin authors who want it.

## Consequences

### Positive

- The common case (`ra tunnel marimo`) is one command with no flags.
- Plugin authors get a one-line `port: N` opt-in; existing plugins are
  unaffected (the field is optional with a meaningful zero value).
- Auto-start parity with `ra connect` means users don't need to remember
  whether the workstation is running before tunneling.
- The `ensureRunning` extraction means `connect` and `tunnel` can't drift
  on the start-if-stopped behavior.

### Negative

- The CLI now has a positional argument with three syntactic shapes. The
  `--help` text and the error path when the user typo's a plugin name (we
  fall through to "no plugin named X declares a port") are the mitigations.
- A future plugin command framework, if we adopt one, may want to absorb
  `ra tunnel` — that migration is a forward-only rename, since the
  `port:` field remains useful regardless of command surface.
- Tunnels are foreground-only. Users who want multiple simultaneous tunnels
  need multiple terminals (or shell job control). Acceptable for v1.

### Neutral

- `port:` lives at the top level of `plugin.yaml`, not nested under a
  `network:` or `tunnel:` block. If we later add per-plugin networking
  config (HTTP path prefix, scheme, healthcheck endpoint), we may move it.

## Verification

1. `go build ./...` and `go test ./...` pass.
2. `ra tunnel --help` shows the three target forms.
3. `ra plugin install ./plugins/marimo` writes
   `plugins.marimo.port-for-tunnel: 2718` into `config.yaml`. (Verified
   via `TestPrompt_AutoInjectsPortForTunnel`.)
4. The configured port is exported as
   `RA_PLUGIN_MARIMO_PORT_FOR_TUNNEL=<value>` into the container env, so
   marimo's `250_marimo-start.sh` binds to it. (Verified via
   `TestFilePlugin_ContainerEnvPairsFrom_PortForTunnel`.)
5. `ra tunnel marimo` shells out to `gcloud workstations start-tcp-tunnel
   ... --local-host-port=localhost:2718 <ws> 2718`. (Verified via
   `TestClient_StartTCPTunnel_CallsCorrectArgs` and
   `TestRunTunnel_Running_CallsStartTCPTunnel`.)
6. If the user customizes `plugins.marimo.port-for-tunnel: 8000` in
   `config.yaml`, `ra tunnel marimo` targets 8000 — same value the
   server is bound to. (Verified via
   `TestParseTunnelTarget_NamedPlugin_ConfigOverridesSchemaDefault`.)
7. `ra tunnel <unknown-name>` errors with the list of installed
   tunnelable plugins. (Verified via
   `TestParseTunnelTarget_UnknownPlugin`.)
8. Against a stopped workstation, `ra tunnel ...` prints `starting...` and
   then opens the tunnel. (Verified via `TestRunTunnel_Stopped_StartsFirst`.)
