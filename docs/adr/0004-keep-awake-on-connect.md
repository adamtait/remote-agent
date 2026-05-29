# ADR 0004: Keep the local machine awake during `ra connect` / `ra tunnel`

- **Status:** Accepted
- **Date:** 2026-05-29
- **Deciders:** @adamtait

## Context

After [ADR 0003] baked a server-side sshd keepalive into the workstation image,
`ra connect` sessions were *still* reported as "constantly losing" the
connection with the familiar:

```
ERROR: Connection to Cloud Workstation lost.
```

Controlled testing against a freshly created workstation (image confirmed to
carry `ClientAliveInterval 15`, `Include` active, sshd honoring it) showed the
server-side fix is working exactly as designed:

| Path | Idle duration | Result |
|---|---|---|
| `gcloud workstations ssh --command` (non-PTY) | 360s | survives |
| `gcloud workstations ssh --command` (non-PTY) | 720s | survives |
| interactive PTY (tmux + Claude TUI) | ~200s | survives |
| TCP tunnel, held connection | 150s | survives |

A *connected* idle session stays up for 12+ minutes on keepalive traffic alone.
So the reap window has not tightened, the image is not stale, and no Dockerfile
override is shadowing the keepalive — all ruled out.

The reporter's signature was the tell: drops happen via `ra connect`, **when
idle / walked away**, after **a few minutes**. That is irreconcilable with a
connected session surviving 12 min — *unless the client itself goes away*.

**Root cause: the local machine idle-sleeps.** gcloud's IAP tunnel relay (the
websocket↔localhost forwarder) runs on the laptop. When macOS idle-sleeps —
the default Energy Saver fires after a few minutes when you step away from the
keyboard, lid open — the relay process and its socket freeze. No traffic flows
on the tunnel in either direction, so the backend reaps the idle websocket after
~30s (the exact mechanism ADR 0003 documented). On wake, gcloud's relay finds
its connection dead and prints `Connection to Cloud Workstation lost.`

The server-side keepalive is powerless here: `ClientAliveInterval` makes *sshd*
send probes, but the frozen client never replies, so no traffic crosses the
tunnel. The fix must keep the **client** from going away.

### Constraints

- The lever is local power management; the workstation can do nothing about a
  sleeping laptop.
- `ra` shells out to `gcloud`, which owns the SSH/tunnel invocation. A wrapper
  process is the clean injection point, not a gcloud flag.
- A dropped session loses no work: the workstation runs a persistent tmux
  session (tmux plugin auto-attach), so reconnecting reattaches. The goal is to
  stop the *churn*, not to prevent data loss.

## Decision

For the lifetime of a foreground connection (`ra connect`, `ra tunnel`), hold a
"prevent idle system sleep" assertion on macOS via `caffeinate`:

```
caffeinate -i -w <ra-pid>
```

- `-i` asserts `PreventUserIdleSystemSleep` — blocks idle sleep, so the relay
  keeps running and keepalives keep flowing while you're away.
- `-w <ra-pid>` makes `caffeinate` self-terminate when `ra` exits, so a killed
  or crashed `ra` never leaves a dangling assertion (belt-and-suspenders
  alongside the deferred `stop()` that kills it on normal return).

**On by default**, with `--no-keep-awake` to opt out. Default-on is deliberate:
the drops are the reported problem, and an opt-in flag only helps users who
already know it exists. The cost is bounded — `-i` does not prevent *lid-close*
sleep, only idle sleep, so battery is preserved when you actually shut the lid;
and the assertion is released the moment the session ends.

The implementation is a no-op on non-darwin platforms and when `caffeinate` is
absent from `PATH`, so the connection path keeps working everywhere.

## Alternatives Considered

### 1. Document a manual `caffeinate` / Energy Saver tweak
Rejected as the primary fix — it does not actually fix anything until the user
discovers it. Kept as supporting docs (README troubleshooting).

### 2. `ra`-managed auto-reconnect on drop
Rejected as heavier and unnecessary. tmux already preserves the session across
reconnects, so the only loss is the manual re-run of `ra connect`. Preventing
the trigger is a smaller, lower-risk change than a reconnect/supervision loop.

### 3. `caffeinate -d` (also prevent display sleep) or prevent lid-close sleep
Rejected — out of scope and more intrusive. Display sleep alone does not drop
the network; *system* sleep does, and `-i` covers exactly that. Preventing
lid-close sleep would be a surprising power-management change.

### 4. Default-off (opt-in `--keep-awake`)
Rejected — see Decision. An opt-in flag leaves the default experience broken.

## Consequences

### Positive
- Unattended `ra connect` / `ra tunnel` sessions (lid open) no longer drop a few
  minutes after you walk away.
- Pure client-side wrapper; no workstation/image change, no new runtime state
  beyond a child process tied to `ra`'s lifetime.
- Degrades cleanly: no-op off-darwin or without `caffeinate`.

### Negative
- While connected with the lid open, the Mac won't idle-sleep — a deliberate
  battery trade-off. `--no-keep-awake` restores stock behavior.
- macOS-only. Linux laptops (e.g. `systemd-inhibit`) are not covered yet; the
  hook (`keepAwakeCommand`) is structured to add a case when needed.

### Neutral
- Closing the lid still sleeps and still drops the tunnel; tmux makes that a
  no-loss reconnect rather than lost work.

## Verification

- Unit: `keepAwakeCommand("darwin", pid)` → `caffeinate -i -w <pid>`;
  unsupported platforms return `ok=false`; `keepAwake(false, …)` is a no-op and
  its `stop()` is idempotent.
- Live: `ra connect` spawns `/usr/bin/caffeinate -i -w <ra-pid>` as a child;
  `pmset -g assertions` shows `PreventUserIdleSystemSleep` held by it; killing
  `ra` reaps the `caffeinate` child via `-w`.
- `go build ./...` / `go vet ./cmd/` / `go test ./...` pass.

[ADR 0003]: 0003-ssh-keepalive-iap-idle-reap.md
