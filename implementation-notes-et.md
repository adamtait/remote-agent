# ET support — implementation notes

Running log of decisions, deviations from the plan, and tradeoffs while
implementing Eternal Terminal (ET) support.
Plan: `~/.claude/plans/build-a-plan-for-lucky-micali.md`.

> **Filename note:** the repo already tracks an unrelated `implementation-notes.md`
> (from the prior open-sourcing effort). To avoid clobbering that committed file,
> these notes live in `implementation-notes-et.md`.

Newest entries at the top of each section.

---

## Follow-up fix: prompt exit on remote `exit` (`fix/et-prompt-exit`)

**Symptom:** after the remote tmux/shell `exit`s, `ra connect --et` kept running
(`[exited]` / `Session terminated` then nothing) until the user pressed Ctrl-C.

**Root cause (verified live against `personal`, not guessed):**
- `ra` runs `et` in the foreground; `ra`'s lifetime == `et`'s. So the question
  is "why is `et` slow to exit," and the teardown after `et` returns.
- **Teardown is fine.** Reproduced `StartTCPTunnelContext`'s exact wiring
  (`exec.CommandContext` + `io.MultiWriter` stderr + `os.Stdin`) in a standalone
  Go program: ctx-cancel kills the gcloud tunnel in **~10ms** with **zero**
  orphaned processes. Once `et` returns, `ra` exits immediately.
- **`et` is slow to return.** From the real et client log: on session end `et`
  logs `Missed a keepalive, killing connection` → `starting new reconnect
  thread` → `Got invalid key on reconnect, assume that server has terminated the
  session` → `Shutting down connection`. It learns the session ended **via a
  keepalive timeout**, not a socket close, because gcloud's `start-tcp-tunnel`
  keeps the **local** socket open after the remote etserver session goes away.
  An interactive `expect` repro measured **~18s** wall (≈12-14s of real `et`
  delay) to exit on a *healthy* tunnel — exactly reproducing the user's output.

**Fix:** pass `et -k <secs>` to lower the client keepalive (new
`ETOptions.KeepaliveSecs`, emitted by `buildETArgs`; default
`etKeepaliveSecs = 3` in `cmd/connect.go`, down from et's ~6s). Detection time
scales with the keepalive interval, so this roughly halves the perceived hang
(~15s → ~3-6s) and additionally speeds recovery from genuine mid-session drops.
Measured live: `-k 2` produces a stable ~3s keepalive cadence with no spurious
reconnects on the IAP relay (sub-second RTT); picked 3 for a small safety
margin.

**Considered and rejected:** an out-of-band watchdog that polls the box (e.g.
`pgrep etterminal`) over the independent IAP ssh path and cancels when the
session is gone. It would exit ~instantly but adds a gcloud round-trip every few
seconds for the *entire* session — exactly the workstation load this project has
been reducing — so the cheap, native keepalive knob wins. Recorded in ADR 0004.

---

## Follow-up fix: cold-start race (`fix/et-server-readiness`)

First live run of `ra connect --et` (workstation freshly started) failed with
`The workstation does not have a server running on port 2022` → et auth error.

**Root cause:** `runConnectET` waited for sshd (port 22) but not for etserver
(port 2022). etserver is launched by the plugin's boot script *after*
`200_remote-agent-setup.sh` fetches secrets, so it comes up tens of seconds
after the workstation is SSH-ready. The data tunnel raced ahead, gcloud found no
server on 2022, and `waitForPortsBound` (a *local* bind check) let `et` launch
into a dead data channel. Confirmed by direct investigation: etserver was
installed, enabled, and listening on `*:2022` — just not yet at connect time;
the tunnel worked once it came up.

**Fix (CLI):** added `Client.ETServerReady` (a `/dev/tcp` port probe over the
existing sshd, sentinel-verified like `SSHReady`) and `waitETServerReady`, called
in `runConnectET` after `waitSSHReady`. Unlike `waitSSHReady` (warn-and-proceed),
this one *gates*: if etserver never appears within 120s it returns an actionable
error instead of handing `et` a doomed connection. Probe validated live against
the workstation (port 2022 → ready, dead port → not ready).

**Also found (corrected):** `ss` is not "broken" — it is **not installed** in the
workstation image (`command not found`; `iproute2`/`net-tools` are absent). The
earlier `2>/dev/null` hid the error and made it look like `ss` returned empty.
The plugin's original `command -v ss && ss …` guard therefore short-circuited and
the collision check was a silent no-op. `/proc/net/tcp[6]` *is* present and
correct, so both the CLI readiness probe and the plugin collision check read the
kernel's own socket table directly (LISTEN = state `0A`) — passive,
dependency-free, and exactly what `ss` would read. The et plugin also now installs
`iproute2` so `ss` exists for interactive debugging (the checks don't depend on
it). The CLI probe uses awk over `/proc/net/tcp`+`tcp6`, with the sentinel
computed by awk (`42*2`) to keep the self-match guard.

---

## Spike findings (Phase 1)

**Result: ET is viable through gcloud workstations tunnels. Gating risk cleared.**

Validated live against the `personal` workstation (config `email-agent-config`).

- **Confirmed working `et` invocation** (this is what `buildETArgs` must produce):
  ```
  et --ssh-option Port=<LA> \
     --ssh-option IdentityFile=$HOME/.ssh/google_compute_engine \
     --ssh-option StrictHostKeyChecking=no \
     --ssh-option UserKnownHostsFile=/dev/null \
     -p <LB> \
     user@localhost
  ```
  where `<LA>` = local bootstrap-tunnel port (→ ws:22), `<LB>` = local data-tunnel
  port (→ ws:2022). The session connected and even auto-attached the tmux `main`
  session, confirming the full path works.
- **Bootstrap-port conveyance:** `--ssh-option Port=<LA>` is sufficient. The
  ssh_config-`Port`-ignored bug only affects config files; a command-line `-o Port=`
  (which is what `--ssh-option Port=` becomes) is honored. **No ProxyCommand alias
  needed** — simpler than the plan's fallback.
- **Auth:** workstation user is `user`; the key gcloud uses /
  the workstation authorizes is `~/.ssh/google_compute_engine`. gcloud's own ssh
  passes no `-i` (relies on default/agent resolution), but passing
  `IdentityFile=~/.ssh/google_compute_engine` explicitly is reliable and matches the
  working raw-ssh test. Also pass `StrictHostKeyChecking=no` +
  `UserKnownHostsFile=/dev/null` (gcloud does the same — host key changes per boot).
- **Data port:** ET's `-p` is the **local** data-tunnel port (ET connects to
  `localhost:<LB>`); host is `localhost`.
- **No systemd in the container:** PID 1 is gcloud's entrypoint, not systemd
  (`systemctl` → "System has not been booted with systemd"). The apt package installs
  an `et.service` unit that will **never run**. The plugin therefore must launch
  `etserver` from `workstation-startup.d/` directly (marimo pattern), not via systemd.
- **etserver telemetry:** prints a telemetry notice; set `ET_NO_TELEMETRY=1` in the
  plugin startup to disable it cleanly.
- **Versions:** local client `et` 6.2.11 (brew formula is `et`, *not*
  `eternalterminal`); box `et` 6.2.10 (PPA `ppa:jgmath2000/et`).
- **Resilience (BackedReader/Writer) not separately stress-tested** in the spike
  (requires an interactive session + mid-session tunnel kill). Core connectivity is
  proven; auto-resume relies on ET's documented resend buffer + the supervised LB
  tunnel loop, and is covered by the end-to-end manual verification step.

## Decisions not in the spec

- **`implementation-notes-et.md`, not `implementation-notes.md`.** The repo
  already tracks a committed `implementation-notes.md` from the prior
  open-sourcing work; I kept these notes in a separate file rather than clobber
  it.
- **`RunContext` on the `Runner` interface (not a bypass).** Implemented in
  `RealRunner` + `LoggingRunner` and added to all five test fakes
  (testRunner, two `fakeRunner`s, retryFakeRunner, secrets fakeRunner). For the
  LoggingRunner a ctx-driven kill logs at Debug ("gcloud run-context ended"),
  not Error — a supervised-tunnel teardown is normal, not a failure.
- **`et` invocation (from the spike).** `--ssh-option Port=<LA>` conveys the
  bootstrap port (the simpler of the plan's two candidates; no ProxyCommand
  alias needed). Identity = `~/.ssh/google_compute_engine` if present (else let
  ssh resolve), user = `user`, plus `StrictHostKeyChecking=no` +
  `UserKnownHostsFile=/dev/null`. All centralized in `buildETArgs`.
- **Wrote ADR 0004** documenting the decision (the repo documents connection
  decisions as ADRs — 0001 tunnel, 0003 keepalive).
- **et plugin port flag.** `etserver` defaults to port 0, so the startup script
  passes `--port` explicitly (verified `etserver --help` on the box). Telemetry
  disabled via `ET_NO_TELEMETRY=1`.
- **Dropped the apt-installed systemd unit** in the plugin's install script —
  the workstation container has no systemd (PID 1 is the gcloud entrypoint), so
  the unit would never run and only invites confusion. etserver is launched from
  `workstation-startup.d/250_et-start.sh` (marimo `setsid` pattern), as root
  (it owns the port and spawns per-user shells via the sshd-authed handshake).

## Deviations from the plan

- **`--ssh-option Port=` was sufficient** — the plan hedged with a ProxyCommand
  fallback in case ET ignored the port. The spike proved the command-line `-o`
  is honored, so the fallback is unused. Recorded for posterity in the ADR.
- **No `etUser` config plumbing.** The plan floated deriving the SSH user from
  config; the image user is a fixed `user`, so it's a const (`etWorkstationUser`)
  with a comment, not a config field.
- **LA (bootstrap) tunnel is persistent**, per the plan's v1 choice — kept up
  for the whole session via the same supervised loop as the data tunnel, rather
  than torn down after the handshake. Simpler; one extra idle gcloud process.

## Tradeoffs

- **Fixed local ports across respawns.** ET only knows the one data-port number,
  so the supervised loop reuses the same `localhost:<port>` on respawn rather
  than picking a fresh one. The `backoffInterval` (500 ms) doubles as the
  port-release window before re-bind; `waitForPortsBound` surfaces a clean error
  if the port is stolen in between.
- **SIGINT handling is best-effort.** `signal.NotifyContext(os.Interrupt)` is
  installed for teardown/orphan-cleanup, but during an active session `et` holds
  the terminal in raw mode, so an in-session Ctrl-C is forwarded to the remote
  shell and never reaches us — which is the desired behavior. The handler mainly
  matters for aborting before connect and cleaning up child tunnels on signal.
- **Two extra gcloud processes** run per `--et` session (one tunnel per remote
  port). Acceptable for the resilience gained; both are torn down on exit.

## Code review outcome (/code-review, high effort)

Ran the multi-angle review on PR #137. **No confirmed correctness bugs.** The
candidates surfaced were verified against the code and refuted:
- "allBound connection leak" — false: each dial is `Close()`d inside the loop
  before the next iteration.
- "redundant cancel() before wg.Wait()" — false: the explicit cancel is required
  to unblock the supervise goroutines (which block in `t.Tunnel(ctx)`); without
  it wg.Wait() deadlocks since `defer cancel()` hasn't fired yet.
- "concurrent Fprintf garbling" — `*os.File.Write` is goroutine-safe and atomic
  per call; `-race` is clean.
- "stderr should be os.Stderr not errOut" — errOut *is* os.Stderr; et drives the
  tty via stdin/stdout.
- "hardcoded user/identity" — deliberate, documented spike findings with a safe
  fallback; not a defect.

No code changes applied (fabricating fixes against refuted findings would churn
correct code).

## Open / needs follow-up

- **Live end-to-end verification of auto-resume** (kill the data tunnel
  mid-session, confirm ET resyncs) is a manual step — covered by the plan's
  verification section but not by automated tests (it needs a real interactive
  session + real `et`/etserver). Core connectivity was proven in the spike.
- **Spike left a manual etserver + apt install on the `personal` box.** Harmless
  (it vanishes on the next image rebuild), and superseded once the `et` plugin
  is installed and `ra create` reruns.
- **Two PRs** (remote-agent CLI + ra-plugins server) must merge together for the
  feature to be usable end to end; the plugin must be installed and `ra create`
  rerun to bake etserver into the image.
- Consider also lowering `ClientAliveCountMax` (ADR 0003) so plain `ra connect`
  freezes resolve faster — independent of ET, noted in the ADR.
