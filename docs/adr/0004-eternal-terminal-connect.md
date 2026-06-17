# ADR 0004: Eternal Terminal connect path (`ra connect --et`)

- **Status:** Accepted
- **Date:** 2026-06-16
- **Deciders:** @adamtait

## Context

[ADR 0003](0003-ssh-keepalive-iap-idle-reap.md) stopped idle `ra connect`
sessions from being reaped by baking a server-side `ClientAliveInterval` into
the image. That fix is live and works — but sessions still drop occasionally on
a different cause: the gcloud-workstations websocket/IAP transport itself breaks
(network blips, NAT rebinds, laptop sleep, relay recycling). When it does, the
session **freezes** rather than failing fast, because `gcloud workstations ssh`
hard-codes the *client's* `-o ServerAliveInterval=0` (and OpenSSH honors the
first value, so we cannot override it). The local `ssh` therefore never probes a
half-dead link; it hangs until the server's `ClientAliveCountMax` reaper closes
it (~2.5 min), after which single-shot `ra connect` exits and the user
reconnects by hand. tmux (the `tmux` plugin) preserves the work, but the
freeze + manual reconnect is the pain.

Server keepalive cannot fix this: the break is on the *client's* side of the
relay, and the client cannot keepalive. What's needed is a transport that
detects a dead link quickly and **resumes** the session on reconnect.

### Options considered

- **mosh** — the canonical flaky-link answer, but it needs UDP ingress.
  Workstations expose only the relay's single TCP port; there is no UDP path to
  the box. Not viable.
- **Supervised auto-reconnect of plain `ssh` + tmux** — reconnect-on-drop with a
  retry loop. Simple and dependency-free, but each reconnect is a visible
  re-handshake/re-attach, and it still eats the detection-latency freeze.
- **Eternal Terminal (ET)** — a TCP-based, mosh-class protocol that keeps an
  encrypted resend buffer, detects a dead link via its own heartbeat
  (independent of ssh's crippled keepalive), and resumes the session instantly
  on reconnect with no re-login. Chosen.

### Why ET is awkward here (and how we work around it)

ET needs **two** connections to the workstation: an **SSH bootstrap to port 22**
(the `et` client shells out to `ssh`, runs `etterminal`, exchanges a session
key) and a persistent **data channel to port 2022** (etserver). But
`gcloud workstations start-tcp-tunnel` forwards **one** local TCP port to one
remote port. So the connect path opens **two** local tunnels (→ ws:22 and
→ ws:2022) and points `et` at them. A Phase-1 spike against a live workstation
confirmed the wiring:

- The bootstrap port is conveyed with `et --ssh-option Port=<local-22-tunnel>`.
  ET ignores ssh_config's `Port`, but a command-line `-o Port=` is honored, so
  **no ProxyCommand alias is needed**.
- Auth reuses gcloud's own workstation SSH identity
  (`~/.ssh/google_compute_engine`, user `user`), with
  `StrictHostKeyChecking=no` / `UserKnownHostsFile=/dev/null` (the host key
  changes every boot — same flags gcloud itself passes).
- The **data tunnel is supervised** (auto-restarted on drop): ET reconnects its
  data channel to the same `localhost:<port>`, which is dead if the underlying
  gcloud websocket dropped, so a respawn loop keeps that local endpoint live and
  ET resyncs through it.

ET therefore improves **recovery** (a drop becomes an automatic, lossless
resume), not the underlying drop *rate* — it still rides on the gcloud tunnel.

## Decision

Add an opt-in `--et` flag to `ra connect`. When set, after the usual
resolve/start/SSH-ready front-half, the CLI:

1. Preflights the local `et` binary (friendly error pointing at `brew install
   et` and the `et` plugin if absent).
2. Picks two free local ports and brings up two **supervised** gcloud tunnels
   (bootstrap → ws:22, data → ws:2022), waiting for both to accept before
   launching `et`.
3. Runs the foreground `et` client; on exit (or SIGINT) tears the tunnels down.

Orchestration lives in `internal/etconn`; the exact `et` invocation is isolated
in one `buildETArgs` function (the spike's landing zone). The server side is an
external **`et` ra-plugin** (installs etserver from the upstream PPA, starts it
at boot from `workstation-startup.d/`, declares `port-for-tunnel: 2022`) —
workstations don't run systemd, so the plugin launches etserver directly.

`--et` is **opt-in**, not the default: it adds a local dependency (`et`) and a
server component (the plugin), and plain `ra connect` remains correct for users
who don't install them.

## Consequences

- Idle/intermittent drops on `ra connect --et` auto-resume in ~seconds with no
  lost session, no re-login, and no manual reconnect.
- Two extra gcloud-tunnel processes run for the session's duration (one per
  remote port); both are torn down on exit.
- New moving parts: the `et` client locally and etserver on the box. Both are
  opt-in; absence yields an actionable error rather than a broken default.
- ET does not reduce the drop *rate*; consider also tightening
  `ClientAliveCountMax` (ADR 0003) so plain `ra connect` freezes resolve faster.
- The `et` invocation depends on gcloud's managed SSH identity/user; if those
  change, only `buildETArgs` / the `etIdentityFile`/`etWorkstationUser` helpers
  in `cmd/connect.go` need updating.
