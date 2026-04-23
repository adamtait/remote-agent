# Claude Code on Google Cloud Workstations (v2.1)

Provisions a browser-accessible dev environment with Claude Code preinstalled
and a private GitHub repo cloned and ready to go.

## What's new in v2.1

**Zero secrets in the workstation config.** All credentials — both Claude
and GitHub — live only in Secret Manager. The container resolves them at
runtime via its attached service account:

- **Claude API key** uses an `apiKeyHelper` script. Claude Code caches the
  result for ~5 minutes, so rotating propagates within that window with no
  restart.
- **Claude OAuth** is fetched at each container boot and written to
  `/run/ws/env` (a tmpfs) and `/etc/environment`. Rotation →
  `workstations stop/start`. In OAuth mode, the entrypoint also seeds
  `~/.claude.json` from a second secret holding the `oauthAccount` block
  so interactive `claude` skips the browser-based onboarding wizard
  (which can't complete on a headless VM).
- **GitHub PAT** uses a custom `credential.helper` script that calls
  `gcloud secrets versions access` on every git operation. Rotations are
  picked up immediately on the next push/pull/clone, no restart.

The workstation config carries only pointers (GCP secret names and env var
names) — no sensitive values. Auth mode is chosen at `ws setup` time by
selecting `claude_oauth` or `claude_apikey` as the Claude secret type.

## Files

- `main.go` / `cmd/` / `internal/` — `ws` CLI (idempotent provisioning)
- `Makefile` — `make install` builds and drops the binary into `~/.local/bin`
- `.ws/Dockerfile` — custom image: code-oss + Node 20 + gh + Claude Code
- `.ws/entrypoint.sh` — runs at container boot; sets up git, clones the repo
- `.ws/profile.d/*.sh` — image-baked login-shell scripts. `00-ws-wait.sh`
  (shipped by `ws setup scaffold`) blocks on the entrypoint's sentinel;
  any additional `*.sh` file you drop here is baked into `/etc/profile.d/`.
- `~/.config/ws/profile.d/*.sh` — same, but scoped to you across all
  projects. On filename collision with a project-level script the
  project-level version wins.

## Prerequisites

- `gcloud` CLI authenticated (`gcloud auth login`)
- `docker` with buildx (for cross-arch builds on macOS)
- A GCP project with billing enabled
- A GitHub fine-grained PAT scoped to the target repo with Contents: Read & write
- If your Google Workspace/Cloud org enforces a session-length / reauth
  policy, the `reauth.googleapis.com` API must be enabled in the workstation
  project. `ws create` enables it automatically; otherwise
  gcloud's OAuth token refresh (e.g. during `gcloud workstations ssh`) will
  fail with `PERMISSION_DENIED: Reauth API has not been used...`.
- **Claude Code auth**, one of:
  - **Claude Pro/Max/Team OAuth token** (recommended — uses your subscription).
    Generate locally with `claude setup-token`, authorize in the browser, copy
    the `sk-ant-oat01-...` value. Valid ~1 year. OAuth mode also needs the
    `oauthAccount` block from your laptop's `~/.claude.json` (see below) so
    the headless VM can skip the onboarding wizard.
  - **Anthropic API key** (pay-as-you-go). Create in the Anthropic Console.

The script prompts on first run to choose. Only one is stored and injected —
setting both in the workstation would cause the API key to override the OAuth
token and silently bypass your subscription.

## One-time setup

Build and install the `ws` CLI, then scaffold configs and run `ws create`:

```bash
make install                # installs ws to ~/.local/bin
ws setup                   # writes .ws/config.yaml + ~/.config/ws/config.yaml
ws create                  # provisions the workstation
```

`ws setup` prompts for:
1. **GCP project ID** — the project where the workstation cluster lives
2. **GitHub repo URL** — the repo to clone inside the workstation
3. **Claude auth mode** — `1` for OAuth token, `2` for API key
4. **Git identity** — name and email for commits inside the workstation

It writes two config files:
- **`.ws/config.yaml`** (committed to source control) — project ID, repo URL,
  and optional per-project overrides
- **`~/.config/ws/config.yaml`** (personal baseline) — all workstation
  parameters, git identity, and secret references

After setup, secrets are pushed to Secret Manager the first time you run
`ws create`. First run takes ~20 minutes (cluster provisioning is the slow
part). Subsequent runs skip everything that already exists and finish in
under a minute.

### Config schema

**`~/.config/ws/config.yaml`** (personal, exhaustive baseline):
```yaml
git:
  user_name: "Alice"
  user_email: "alice@example.com"

workstation:
  region: us-central1
  machine_type: e2-standard-4
  disk_size_gb: 50
  cluster_name: claude-dev-cluster
  # ... other workstation parameters

secrets:
  claude_oauth:            # or claude_apikey — exactly one required
    secret_name: claude-code-oauth-token
    env_var: CLAUDE_CODE_OAUTH_TOKEN
  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN

env: {}
```

**`.ws/config.yaml`** (project-specific, thin overrides):
```yaml
project:
  id: my-gcp-project
  github_repo_url: https://github.com/org/repo.git

# Optional per-project overrides:
# workstation:
#   machine_type: e2-standard-8
# secrets:
#   my_api:
#     secret_name: my-api-key
#     env_var: MY_API_KEY
# env:
#   MY_VAR: value
```

Config is layered: CLI args override project config, which overrides user
config.

## Day-to-day

```bash
ws list           # list workstations
ws url            # print the browser IDE URL
ws connect        # SSH into the workstation
ws logs           # tail workstation logs
ws stop           # stop (save $)
ws delete         # delete the workstation
```

Each targeted command accepts an optional name argument; otherwise the
default `<project_id>-<username>` workstation is used.

Open the printed browser IDE URL, launch a terminal, `cd ~/workspace`, run
`claude`.

## Rotating credentials

All rotations happen via Secret Manager — no need to re-run `ws create`.
The GCP secret names are the `secret_name` values from your
`~/.config/ws/config.yaml` (defaults shown below).

```bash
# Claude API key — auto-picked up within ~5 min, no restart
echo -n 'sk-ant-newvalue' | gcloud secrets versions add <claude_apikey secret_name> \
    --data-file=- --project=$PROJECT_ID

# Claude OAuth token — picked up on next `workstations start`
echo -n 'sk-ant-oat01-newvalue' | gcloud secrets versions add <claude_oauth secret_name> \
    --data-file=- --project=$PROJECT_ID
ws stop
ws create    # starts the workstation back up

# GitHub PAT — picked up on next git operation, no restart
echo -n 'ghp_newvalue' | gcloud secrets versions add <github_pat secret_name> \
    --data-file=- --project=$PROJECT_ID
```

To switch Claude auth modes (OAuth ↔ API key), re-run `ws setup` to update
the secret type in `~/.config/ws/config.yaml`, then re-run `ws create` and
restart the workstation.

## How auth actually works at runtime

Auth mode is determined at `ws setup` time by which logical secret name you
configure in `~/.config/ws/config.yaml`: `claude_oauth` or `claude_apikey`.
The container receives the chosen secret name(s) via environment variables at
boot.

**Claude API key mode** (`claude_apikey` in secrets config). At every container
boot, `entrypoint.sh` writes `/usr/local/bin/claude-apikey-helper.sh` (a
one-liner that shells out to `gcloud secrets versions access`) and adds
`"apiKeyHelper": "/usr/local/bin/claude-apikey-helper.sh"` to
`~/.claude/settings.json`. Claude Code calls this script on startup and then
every ~5 minutes, so rotating the secret is transparent.

**Claude OAuth mode** (`claude_oauth` in secrets config). At every container
boot, `entrypoint.sh` fetches the token from Secret Manager and writes it to
`/run/ws/env` (alongside `GH_TOKEN`). `/run/ws` is a tmpfs, so the file never
touches disk. Login shells are held at `/etc/profile.d/00-ws-wait.sh` until
`entrypoint.sh` creates the `/run/ws/ready` sentinel, at which point
`00-ws-wait.sh` sources `/run/ws/env` and the shell proceeds. This closes the
race where sshd begins accepting connections before the entrypoint has written
the token. Non-login sessions (IDE-spawned terminals) continue to read the
token from `/etc/environment` via PAM. Because `apiKeyHelper` conflicts with
OAuth tokens in Claude Code, the entrypoint also removes any stale
`apiKeyHelper` entry from `settings.json` when running in this mode.

In OAuth mode the entrypoint also pre-seeds `/home/user/.claude.json` with
`hasCompletedOnboarding: true`, `lastOnboardingVersion` (from
`claude --version`), and an `oauthAccount` block fetched from a second
Secret Manager secret. Without this, interactive `claude` launches a
browser-based onboarding wizard that can't complete on a headless VM —
even though the OAuth token itself is valid and `claude -p` works. The
seed is merged into any existing `~/.claude.json` so project state
(trusted dirs, MCP servers, etc.) is preserved across boots.

**GitHub PAT** (`github_pat` in secrets config). At every container boot,
`entrypoint.sh` writes `/usr/local/bin/git-credential-gcp-secret`, a script
implementing git's credential helper protocol. On each git operation against
`github.com`, git invokes the helper, which fetches the current PAT from
Secret Manager and prints `username=oauth2\npassword=<token>` to stdout. The
helper ignores other hosts and never persists credentials. `~/.gitconfig` (on
the persistent home disk) points at this helper via
`credential.helper = /usr/local/bin/git-credential-gcp-secret`.

**Additional secrets.** Any other logical name defined in the `secrets:` block
of your config is fetched at boot and exported as the configured `env_var`.
Rotation requires a workstation restart.

## Cost notes

- Cluster: no standby cost when no workstations are running
- Workstation: billed per minute while running (e2-standard-4 ≈ $0.13/hr)
- Auto-stops after 2h idle by default
- Persistent home disk: billed even when stopped (~$0.04/GB-month for pd-standard)

## Security notes

The workstation config carries zero secret values — only secret names and
non-sensitive metadata. Anyone with `workstations.configs.get` can read the
config and learn *which* secrets are in use, but cannot read the secret
values themselves; that requires `secretmanager.secretAccessor`, granted
only to the workstation's service account.

In API-key mode, the Claude key is fetched into memory by `apiKeyHelper`
on demand and never touches disk. In OAuth mode, the token is written to
`/run/ws/env` (mode 0644) and to `/etc/environment`; `/run/ws` is a
tmpfs so only `/etc/environment` survives a reboot — and that file is
rewritten from Secret Manager on the very next boot. The GitHub PAT is
fetched per-operation by the git credential helper and never persisted.

To audit credential access, look at Secret Manager's data access audit
logs — every fetch from `apiKeyHelper`, the OAuth boot fetch, and every
git operation will appear there.
