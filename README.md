# Remote Agent (ra)

**Run your CLI coding agents securely on a throwaway Google Cloud Workstation, not your laptop.**

`ra` provisions a browser- and SSH-accessible [Google Cloud Workstation](https://cloud.google.com/workstations), attaches your credentials through GCP Secret Manager, and drops you into a persistent dev environment where an agent can read untrusted issues and run shell commands without ever touching your ~/.aws/credentials or your email session. It's a single Go binary that wraps gcloud — bring the terminal agent you already use.

```console
$ ra create
# → enables APIs, builds & pushes the image, creates the cluster/config, boots the VM
================================================================
  Workstation is ready
================================================================
  Browser IDE  : https://80-abc123.cluster-def.cloudworkstations.dev
  Workstation  : swift-otter
  Cluster      : ra-cluster
  Config       : ra-config
  Region       : us-central1

  SSH:
    ra connect swift-otter

  Stop (save cost):
    ra stop swift-otter
================================================================

$ ra connect swift-otter          # SSH in, start your agent, close your laptop
```

## Why Remote Agent?

- **Blast radius is a rented VM, not your machine.** A prompt injection in a branch name or GitHub issue can make an agent exfiltrate tokens or `rm -rf` — on a Cloud Workstation that's a disposable box holding none of your personal accounts, not the laptop where you do your banking.
- **Sessions outlive your laptop.** The workstation keeps running in the cloud while you close the lid. Reconnect from anywhere over SSH or the browser IDE; add the `et` plugin for connections that auto-resume after a drop.
- **Bring your own agent, no lock-in.** `ra` ships with *no* built-in tooling. Claude Code, Codex, the GitHub CLI, tmux — each is an external plugin you `ra plugin install`, so nothing is baked in that you didn't choose.
- **Zero secrets in config.** Config files carry only secret *names*; the values live in GCP Secret Manager and are fetched at boot by the workstation's own service account. Nothing sensitive is committed or baked into the image.
- **You own the environment.** It's a real Dockerfile, real IAM, and a real VPC you can shape — not a locked-down hosted sandbox with an on/off network toggle.
- **Claude subscription friendly:** no need to worry about `claude -p` (or other agent tools) being billed at API prices.

## Install

**macOS / Linux (from source)** — the supported path today:

```bash
git clone https://github.com/adamtait/remote-agent
cd remote-agent
make install          # builds ./ra and installs to ~/.local/bin/ra
```

<!-- TODO: publish prebuilt binaries / Homebrew tap / Windows (WSL) instructions -->

**Prerequisites** (all shelled out to, so they must be on your `PATH`):

```bash
gcloud auth login     # authenticated gcloud CLI, on a project with billing enabled
docker --version      # docker with buildx (cross-arch image builds)
git --version         # used to clone plugins
```

## Usage

Scaffold your config once, then create and connect:

```bash
ra setup              # writes ~/.config/ra/config.yaml + core build assets
ra create             # provision a workstation (name auto-generated if omitted)
ra connect            # SSH in — starts the workstation first if it's stopped
```

Add the tools you want — plugins live in their own repo, [`ra-plugins`](https://github.com/adamtait/ra-plugins):

```bash
ra plugin install https://github.com/adamtait/ra-plugins github   # gh CLI + PAT helper + auto-clone
ra plugin install https://github.com/adamtait/ra-plugins claude   # claude CLI + credential helper
ra plugin list                                                    # what's installed, at which level
```

Drive the lifecycle. Every targeted command takes an optional `[name]`; without one it uses the default (or prompts):

```bash
ra list               # NAME / STATE for the cluster's workstations
ra url                # print the browser IDE URL
ra logs               # stream the workstation's container logs
ra stop               # stop to halt per-minute charges (home disk persists)
ra tunnel 8080        # forward a workstation port to localhost:8080
ra delete             # delete one workstation (leaves the shared cluster up)
```

Kill the standing cost when you're done for a while — the cluster control plane bills 24/7 until it's deleted:

```bash
ra teardown --dry-run # preview exactly what gets deleted
ra teardown           # delete workstation → config → cluster → SA/IAM → secrets → registry
```

## Configuration

Two files are merged at startup, project overriding user: `~/.config/ra/config.yaml` (user baseline) and an optional `.ra/config.yaml` (per-project, committed to source control). Workstation knobs live under `gcloud.workstation`:

| Key | Default | What it does |
|---|---|---|
| `auto_sleep_hours` | `2` | Idle time before the workstation auto-stops to save money. `0` = never sleep; range `1`–`24`. |
| `auto_shutdown_hours` | `12` | Max continuous run time before a force-stop, even if busy. Range `1`–`168`. |
| `machine_type` | `e2-standard-2` | VM size (2 vCPU / 8 GiB). Bump to `e2-standard-4`/`-8` for heavier builds. |
| `disk_size_gb` | `50` | Persistent home-disk size. Survives stops; billed even while stopped. |
| `region` | `us-central1` | GCP region the workstation runs in. |

```yaml
gcloud:
  workstation:
    auto_sleep_hours: 1        # stop after 1h idle
    auto_shutdown_hours: 8     # never run longer than 8h at a stretch
    machine_type: e2-standard-4
    disk_size_gb: 100
```

Both timeouts can be overridden per-provision: `ra create --auto-sleep 1 --auto-shutdown 8`. 

Run `ra validate --show` to print the merged config a `ra create` will actually use.


## How it fits together

- **Extension points, lightest first.** Experiment with **live changes** on the running box (lost on rebuild); promote what sticks to **local config** in `~/.config/ra/` or `.ra/` (`Dockerfile.d/`, `workstation-startup.d/`, `profile.d/` merged into every build); package the shareable stuff as a **plugin**. See [Extension Points & Plugins](docs/extensions_and_plugins.md) and the [plugin authoring guide](docs/authoring-plugins.md).
- **Auth at runtime.** At boot, `200_remote-agent-setup.sh` fetches each secret named in `RA_SECRETS` from Secret Manager into `/etc/environment` + `/run/ra/env`; plugin startup scripts run after and gate on `RA_PLUGIN_<NAME>_ENABLED`. Config carries only secret names — reading values requires `secretmanager.secretAccessor`, held solely by the workstation's service account. Audit every fetch in Secret Manager's data-access logs. See [Auth & Security](docs/auth_and_security.md) for more details.
- **`ra plugin` reference.** `install <source> [<path>]` supports `--ref <branch|tag|commit>`, `--user` / `--local` (level), and `--force`; `list` and `remove <name>` round it out. The two-arg form installs one plugin from a multi-plugin repo: `ra plugin install https://github.com/adamtait/ra-plugins github`. Plugins can also add their own subcommands — e.g. `ra plugin et connect` tunnels through [Eternal Terminal](https://eternalterminal.dev/) so a dropped SSH session auto-resumes.
- **Rotating secrets.** Rotate a plugin credential by re-running its install with `--force` (`ra plugin install <source> <name> --force`) or `gcloud secrets versions add`. Per-operation helpers (e.g. the GitHub PAT) and periodic-refresh API keys pick up the new version without a restart; boot-time secrets need an `ra stop` + start to take effect. See [Auth & Security](docs/auth_and_security.md) for details on secret rotation.


## What it costs

The workstation is cheap; the **cluster control plane** is the real line item — it bills a flat ~$0.20/cluster-hour 24/7 whether or not any workstation is awake, and auto-sleep does nothing for it. Consolidate to one shared cluster (it holds many configs and workstations), and `ra teardown` to stop the charge during long idle stretches.

| What | Rate | When billed |
|---|---|---|
| Cluster control plane | ~$4.80 / day (~$144/mo) | continuously, per cluster, until deleted |
| Workstation (`e2-standard-2`) | ~$0.17 / running hour | per minute while running; $0 asleep |
| Persistent home disk (50 GB) | ~$0.16 / day | continuously, even while stopped |

A typical 8-hour coding day on one cluster runs **~$6/day** (~$5 base + ~$1.34 running). The single biggest lever is cluster count — the intro post's cost appendix has the full breakdown and sources.
<!-- TODO: link the published intro post / cost appendix once it has a URL -->

## Comparison

| Feature | Remote Agent | Claude Managed Agents | Devin | Cursor Cloud |
|---|---|---|---|---|
| **Underlying Compute** | GCP Cloud Workstations | BYO Compute | Hosted Environment | Hosted Environment |
| **Custom Dockerfile** | ✅ Yes | ❌ No | ❌ No (Setup scripts only) | ✅ Yes |
| **Network Egress Controls** | ✅ Full VPC control | ⚠️ Depends on BYO | ❌ Fixed defaults | ❌ Fixed defaults |
| **Agent CLI choice** | ✅ Bring any terminal agent | ✅ Web UI / API | ✅ Custom agent | ✅ Custom agent |


## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for build instructions and local testing workflows. To report a security vulnerability, please see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © Adam Tait
