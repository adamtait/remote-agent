# ADR 0015: `et` plugin connect — python3 local-port helpers (avoid a macOS bash SIGABRT)

- **Status:** Accepted
- **Date:** 2026-07-09
- **Deciders:** @adamtait

## Context

The `et` plugin's `commands/connect.sh` (the local orchestrator behind
`ra plugin et connect` — see [ADR 0011](0011-eternal-terminal-connect.md) for the
two-tunnel design and [ADR 0014](0014-connection-config-and-plugin-commands.md)
for the move of ET out of core into a plugin command) **crashed with SIGABRT
(exit 134) before it ever launched `et`**, so the command "waited a few seconds,
then returned to the shell" with no session. `ra` maps the non-zero script exit
to `errSilent` (exit 1), so the failure was silent.

Root cause, verified live on a workstation:

- `connect.sh` picks two free local ports (`free_port`) and waits for two
  supervised `ra tunnel` processes to start accepting (`wait_port`). Both used
  bash's `/dev/tcp` pseudo-device in tight loops.
- Those `/dev/tcp` probe loops run **concurrently with the two backgrounded
  `ra tunnel` (Go, multi-threaded) jobs**. Under that concurrency, bash 5.2.26
  on macOS (arm64) aborts with SIGABRT — a fork/allocation race inside bash's
  `/dev/tcp` handling.
- It is timing-sensitive: **~100% at normal speed, 0% under `bash -x`** (tracing
  slows execution enough to dodge the race), which is why it evaded casual
  testing.

Evidence gathered while diagnosing:

| Variant | Result |
|---|---|
| `et` via manually-created tunnels (both `-N` and interactive) | ✅ connects, lands in tmux — the ET transport itself is fine |
| Original `connect.sh`, normal speed ×10 | ❌ 10/10 SIGABRT |
| Same under `bash -x` | ✅ 0 crashes (proves timing race) |
| Remove `set -m` | ❌ still crashes (not job control) |
| Stub out `/dev/tcp` entirely | ✅ 0 crashes |
| Swap `/dev/tcp` → `nc -z` (keep the tight bash loop) | ⚠️ 2/6 — spawning any subprocess per iteration still races |
| **Single-shot `python3` for port-pick and for waiting (no bash spawn loop)** | ✅ 0/8 crashes, full connect into tmux |

The decisive factor is not `/dev/tcp` specifically but **bash spawning
subprocesses in a tight loop while the background tunnel jobs are alive**. `nc`
only halved the crash rate; moving the *entire* poll loop into one long-lived
child eliminated it.

### Options considered

- **Keep `/dev/tcp`, remove `set -m`** — rejected; `set -m` was not the trigger.
- **Swap `/dev/tcp` for `nc -z`** — rejected; still 2/6 crashes because the tight
  bash loop still forks a probe per iteration.
- **Single-shot `python3` port helpers** — chosen. Bash spawns exactly one child
  per operation; the poll loop runs *inside* that child, so there is no bash
  fork storm concurrent with the tunnels.
- **Move the whole tunnel+`et` orchestration into `ra` (Go)** — rejected for now:
  it reverses ADR 0014 (ET deliberately left core), and there is no plugin ABI
  for native code, so it would mean a new subsystem plus re-implementing tunnel
  supervision, TTY hand-off, and teardown in Go with tests. A much larger change
  than the bug warrants; noted as the right long-term shape if `ra` ever wants
  first-class connection transports.

## Decision

In the `et` plugin's `connect.sh`, replace the two `/dev/tcp` bash loops with
single `python3` invocations, and add a `python3` preflight check:

- **`free_port` → one `python3`** that binds two ephemeral sockets
  simultaneously (`bind(("127.0.0.1", 0))`) and prints both OS-assigned ports.
  This also removes the old `RANDOM`-guess-and-probe entirely.
- **`wait_port` → one `python3`** per port that runs its own 30s
  `socket.create_connection` poll loop internally.
- A `command -v python3` guard next to the existing `command -v et` guard, with
  an actionable error.

The plugin gains a `post_install_message` noting the new **local** `python3`
dependency. Everything else in `connect.sh` — the two supervised tunnels, the
etserver-readiness probe (a `gcloud ssh` loop at a low `sleep 3` cadence, left
as-is), the `et` argv, and the EXIT-trap teardown — is unchanged.

No `remote-agent` code changes: `post_install_message` is already supported
(`internal/plugins/schema.go`, printed by `cmd/plugin.go`). This ADR is the only
change in this repo.

## Consequences

- `ra plugin et connect` now reaches `et` reliably and resumes the session on
  drops as designed (0/8 crashes in testing; full connect into tmux verified).
- The plugin now requires **`python3` on the local machine** (client side only,
  not the workstation). It ships with macOS and virtually all Linux dev
  machines; absence yields an actionable error, and the `post_install_message`
  flags it at install time.
- `free_port` no longer guesses with `RANDOM`; the OS assigns guaranteed-free
  ephemeral ports, which is also strictly more correct.
- This is bash-portability hardening specific to macOS; the change is harmless on
  Linux (the `python3` helpers behave identically).
- If a future need arises to drop the `python3` dependency, the remaining
  constraint is documented here: **do not run tight subprocess-spawning loops in
  `connect.sh` while the background `ra tunnel` jobs are alive.**
