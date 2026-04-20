# Claude Code on Google Cloud Workstations

Provisions a browser-accessible dev environment with Claude Code preinstalled
and a private GitHub repo cloned and ready to go.

## Files

- `create-workstation.sh` — main orchestration script (idempotent)
- `Dockerfile` — custom image: code-oss + Node 20 + gh + Claude Code
- `entrypoint.sh` — runs at container boot; sets up git, clones the repo

## Prerequisites

- `gcloud` CLI authenticated (`gcloud auth login`)
- `docker` with buildx (for cross-arch builds on macOS)
- A GCP project with billing enabled
- A GitHub fine-grained PAT scoped to the target repo with Contents: Read & write

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
You'll be prompted for your `ANTHROPIC_API_KEY` and GitHub PAT — these go
straight into Secret Manager, not your shell history.

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

## Rotating the GitHub PAT

```bash
echo -n 'ghp_newvalue' | gcloud secrets versions add github-pat \
    --data-file=- --project=$PROJECT_ID
./create-workstation.sh    # re-reads latest version into the config
```

## Cost notes

- Cluster: no standby cost when no workstations are running
- Workstation: billed per minute while running (e2-standard-4 ≈ $0.13/hr)
- Auto-stops after 2h idle by default
- Persistent home disk: billed even when stopped (~$0.04/GB-month for pd-standard)

## Security notes

Secrets are injected as container env vars via `--container-env`. Only
principals with `workstations.configs.get` can read the config, so this is
acceptable for dev environments. For production/shared environments, replace
the env-var injection with a startup-script fetch from the metadata server
and Secret Manager REST API.
