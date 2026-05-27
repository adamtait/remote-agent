# ADR 0003: SSH keepalive to survive the Cloud Workstations IAP idle reap

- **Status:** Accepted
- **Date:** 2026-05-27
- **Deciders:** @adamtait

## Context

`ra connect` (and any `gcloud workstations ssh`) sessions were dropping with:

```
ERROR: Connection to Cloud Workstation lost.
```

The drops felt random and "within seconds" in interactive use, which sent the
investigation down several wrong paths (tmux auto-attach, the Claude TUI,
OpenSSH 10.x keystroke-timing obfuscation, the IAP websocket implementation).
Controlled testing against a live workstation ruled all of those out and
isolated a single deterministic cause:

**The Cloud Workstations IAP/SSH tunnel reaps a connection after ~30 seconds
with no data flowing in either direction.** The `ERROR: Connection to Cloud
Workstation lost.` message is printed by gcloud's local tunnel relay (the
websocket↔localhost forwarder), not by `ssh` — `ssh` debug output continues
*after* the error, i.e. the relay died and `ssh` then saw its localhost socket
close.

Interactive sessions only *felt* like "<5s" because a shell or TUI (e.g.
Claude Code) sitting idle at a prompt produces no traffic and gets reaped ~30s
later.

### Why the earlier fix was insufficient

[PR #128] tried a client-side keepalive (`--ssh-flag=-o ServerAliveInterval=30`).
It is a no-op: `gcloud workstations ssh` hard-codes `-o ServerAliveInterval=0`
*ahead* of any `--ssh-flag` we pass, and OpenSSH honors the first value for a
given `-o` key, so gcloud's `0` wins.

[PR #129] then drove keepalives from the **server** by baking
`ClientAliveInterval 30` / `ClientAliveCountMax 10` into the workstation image's
sshd config. This was the right *layer* but the wrong *value*: a 30s interval
fires its first probe *at* ~30s, racing the ~30s reap — and losing. Idle
sessions still dropped at ~30–35s.

### Constraints

- `ra` shells out to `gcloud` for all GCP interactions ([CLAUDE.md][1]), and
  `gcloud workstations ssh` owns the SSH invocation. We do not control the ssh
  command line beyond appended `--ssh-flag`s.
- **Client-side SSH keepalive is unavailable.** gcloud forces
  `-o ServerAliveInterval=0` first; `--ssh-flag=-o ServerAliveInterval=N` is
  silently ignored (verified empirically — see Verification). The only usable
  lever is **server-side** sshd config baked into the image.
- The keepalive must keep traffic flowing on the tunnel without depending on
  the user typing or an app emitting output.

[1]: ../../CLAUDE.md

## Decision

Bake a server-side keepalive into the embedded workstation image
(`internal/scaffold/assets/Dockerfile`) with an interval comfortably **under**
the ~30s reap window:

```
# /etc/ssh/sshd_config.d/10-ra-keepalive.conf
ClientAliveInterval 15
ClientAliveCountMax 10
```

sshd sends a `ClientAlive` probe after 15s of inactivity; the client's reply is
traffic on the tunnel, which resets the backend's idle-reap timer well before
the ~30s deadline. `ClientAliveCountMax 10` means an *actually* unresponsive
client (laptop asleep, network gone) is tolerated for 15s × 10 = ~2.5 min
before sshd reaps the session server-side.

The drop-in is installed ahead of any conflicting directive: Debian/Ubuntu
sshd `Include`s `sshd_config.d/*.conf` and honors the first value for a keyword,
so the Dockerfile prepends the `Include` line if the base image lacks it.

`15` is chosen for a ~2× safety margin below the observed reap window (idle
survives at 20s, drops at 35s under a 30s interval), while keeping keepalive
chatter trivial (one small packet per 15s of idle).

## Alternatives Considered

### 1. Client-side keepalive (`ServerAliveInterval`)
Rejected — not possible. gcloud hard-codes `-o ServerAliveInterval=0` before
our flags and ssh honors the first value. Confirmed empirically: a 60s idle
session with `--ssh-flag=-oServerAliveInterval=15` still dropped.

### 2. Keep `ClientAliveInterval 30` (the PR #129 value)
Rejected — the bug. The first probe at ~30s races the ~30s reap and loses;
idle sessions still drop at ~30–35s.

### 3. An even shorter interval (e.g. 10s)
Viable, more margin, negligible extra cost. Not chosen because 15s is the value
verified end-to-end and already clears the observed reap window with ~2× margin.
If the reap window is later observed to be more aggressive, lowering to 10s (or
less) is the first knob to turn — no design change required.

### 4. Switch the IAP tunnel websocket implementation
Rejected — not the cause. Both the old and new tunnel websocket
(`CLOUDSDK_COMPUTE_IAP_TUNNEL_USE_NEW_WEBSOCKET`) drop idle sessions identically.

### 5. `ra`-managed reconnect / application-level keepalive
Rejected as out of scope and heavier. `ra` does not manage long-lived process
lifecycles, and a server-side sshd directive is a one-line, zero-runtime-state
fix. Application-level keepalive (e.g. a background pinger) would also not help
the interactive `ra connect` case, where there is no app to emit traffic.

## Consequences

### Positive
- Idle `ra connect` / `ra tunnel` sessions stay up indefinitely; the
  "Connection to Cloud Workstation lost" drops stop.
- Pure server-side fix — no `ra` runtime state, no client config, benefits
  every connection path (`ra connect`, `ra tunnel`, raw `gcloud workstations
  ssh`).
- Faster detection of genuinely-dead clients server-side (~2.5 min vs the old
  ~5 min), so stale sessions/processes are reaped sooner.

### Negative
- The interval is pinned to an *observed* backend behavior (~30s reap), not a
  documented SLA. If Google tightens the reap below ~15s, drops could return;
  the mitigation is to lower the interval (see Alternative 3).
- Slightly more keepalive traffic on idle connections (one tiny packet per 15s).
  Negligible.

### Neutral
- The keepalive lives only in the **embedded** Dockerfile. Existing workstations
  must rebuild the image (`ra create`) to pick up changes, and a stale
  project/user `Dockerfile` override silently shadows it — see [ADR-adjacent]
  PR #130, which warns when an override diverges from the embedded default.

## Verification

Reproduced and fixed against a live workstation (`gcloud workstations ssh
<ws> --command='...'`, measuring idle survival):

| Scenario | Interval | Result |
|---|---|---|
| 20s idle | 30 | survives |
| 35s / 55s / 70s / 90s idle | 30 | **drop** (reap window ≈ 25–30s) |
| traffic every 15s for 90s | 30 | survives (sub-30s traffic defeats the reap) |
| old vs new IAP websocket, 75s idle | 30 | both **drop** (not a websocket issue) |
| client `-oServerAliveInterval=15`, 60s idle | — | **drop** (gcloud's `=0` wins) |
| plain non-PTY `--command` 90s idle | 30 | **drop** (not tmux / TUI / keystrokes / terminal) |
| 60s idle | **15** | **survives** |
| 90s idle | **15** | **survives** |

The 15s result was confirmed by reloading sshd live (`ClientAliveInterval 15`
+ `SIGHUP`) before baking it into the image, which also proved SSH keepalive
*packets* reset the IAP idle timer — the 30s failure was a timing race, not
ignored keepalive.

`go build ./...` / `go test ./...` pass;
`TestAssembleBuildContext_DockerfileFromEmbedded` asserts the embedded
Dockerfile carries `ClientAliveInterval 15`.
