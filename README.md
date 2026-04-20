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
  `/etc/profile.d/claude-auth.sh`. Rotation → `workstations stop/start`.
- **GitHub PAT** uses a custom `credential.helper` script that calls
  `gcloud secrets versions access` on every git operation. Rotations are
  picked up immediately on the next push/pull/clone, no restart.

The `--container-env` block now contains only pointers
(`CLAUDE_AUTH_SECRET_NAME`, `GITHUB_PAT_SECRET_NAME`, `GCP_PROJECT_ID`,
`GITHUB_REPO_URL`, `GIT_USER_NAME`, `GIT_USER_EMAIL`) — no sensitive values.

## Files

- `create-workstation.sh` — main orchestration script (idempotent)
- `Dockerfile` — custom image: code-oss + Node 20 + gh + Claude Code
- `entrypoint.sh` — runs at container boot; sets up git, clones the repo

## Prerequisites

- `gcloud` CLI authenticated (`gcloud auth login`)
- `docker` with buildx (for cross-arch builds on macOS)
- A GCP project with billing enabled
- A GitHub fine-grained PAT scoped to the target repo with Contents: Read & write
- **Claude Code auth**, one of:
  - **Claude Pro/Max/Team OAuth token** (recommended — uses your subscription).
    Generate locally with `claude setup-token`, authorize in the browser, copy
    the `sk-ant-oat01-...` value. Valid ~1 year.
  - **Anthropic API key** (pay-as-you-go). Create in the Anthropic Console.

The script prompts on first run to choose. Only one is stored and injected —
setting both in the workstation would cause the API key to override the OAuth
token and silently bypass your subscription.

## One-time setup

Put all three files in the same directory, then:

```bash
export PROJECT_ID=my-gcp-project
export GITHUB_REPO_URL=https://github.com/my-org/my-repo.git
export GIT_USER_NAME="Adam Example"
export GIT_USER_EMAIL="adam@example.com"

chmod +x create-workstation.sh entrypoint.sh
./create-workstation.sh
```

First run takes ~20 minutes (the cluster provisioning is the slow part).
You'll be prompted to choose OAuth token vs API key, then for the value and
your GitHub PAT — these go straight into Secret Manager, not your shell
history.

Subsequent runs skip everything that already exists and finish in under a
minute. Re-run after rotating a secret to pick up the new value in the
workstation config.

## Day-to-day

```bash
# Start
gcloud workstations start NAME --cluster=... --config=... --region=...

# Stop (save $)
gcloud workstations stop  NAME --cluster=... --config=... --region=...
```

Open the printed browser IDE URL, launch a terminal, `cd ~/workspace`, run
`claude`.

## Rotating credentials

All rotations happen via Secret Manager — no need to re-run the script.

```bash
# Claude API key — auto-picked up within ~5 min, no restart
echo -n 'sk-ant-newvalue' | gcloud secrets versions add anthropic-api-key \
    --data-file=- --project=$PROJECT_ID

# Claude OAuth token — picked up on next `workstations start`
echo -n 'sk-ant-oat01-newvalue' | gcloud secrets versions add claude-code-oauth-token \
    --data-file=- --project=$PROJECT_ID
gcloud workstations stop NAME  --cluster=... --config=... --region=...
gcloud workstations start NAME --cluster=... --config=... --region=...

# GitHub PAT — picked up on next git operation, no restart
echo -n 'ghp_newvalue' | gcloud secrets versions add github-pat \
    --data-file=- --project=$PROJECT_ID
```

To switch Claude auth modes (OAuth ↔ API key), delete the old secret first
and re-run the script:

```bash
gcloud secrets delete anthropic-api-key --project=$PROJECT_ID
./create-workstation.sh    # will prompt for OAuth token
gcloud workstations stop NAME ... && gcloud workstations start NAME ...
```

## How auth actually works at runtime

**Claude API key mode.** At every container boot, `entrypoint.sh` writes
`/usr/local/bin/claude-apikey-helper.sh` (a one-liner that shells out to
`gcloud secrets versions access latest --secret=anthropic-api-key`) and adds
`"apiKeyHelper": "/usr/local/bin/claude-apikey-helper.sh"` to
`~/.claude/settings.json`. Claude Code calls this script on startup and then
every ~5 minutes, so rotating the secret is transparent.

**Claude OAuth mode.** At every container boot, `entrypoint.sh` fetches the
token from Secret Manager and writes it to `/etc/profile.d/claude-auth.sh`
as `export CLAUDE_CODE_OAUTH_TOKEN=...`. The code-oss terminal is a login
shell so it picks this up automatically. Because `apiKeyHelper` conflicts
with OAuth tokens in Claude Code, the entrypoint also removes any stale
`apiKeyHelper` entry from `settings.json` when running in this mode.

**GitHub PAT.** At every container boot, `entrypoint.sh` writes
`/usr/local/bin/git-credential-gcp-secret`, a script implementing git's
credential helper protocol. On each git operation against `github.com`,
git invokes the helper, which fetches the current PAT from Secret Manager
and prints `username=oauth2\npassword=<token>` to stdout. The helper
ignores other hosts and never persists credentials. `~/.gitconfig` (on
the persistent home disk) points at this helper via
`credential.helper = /usr/local/bin/git-credential-gcp-secret`.

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
`/etc/profile.d/claude-auth.sh` (mode 0644) because exporting a value into
login shells requires a file the shell can read; this is equivalent in
exposure to an env var. The GitHub PAT is fetched per-operation by the git
credential helper and never persisted.

To audit credential access, look at Secret Manager's data access audit
logs — every fetch from `apiKeyHelper`, the OAuth boot fetch, and every
git operation will appear there.
