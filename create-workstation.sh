#!/usr/bin/env bash
#
# create-workstation.sh  (v2.1)
# Provisions a Google Cloud Workstation preloaded with Claude Code
# and a clone of a private GitHub repo.
#
# Auth model (v2.1 — zero secrets in workstation config):
#   - All credentials are fetched at runtime from Secret Manager via the
#     workstation VM's attached service account. The config carries only
#     pointers (secret names) and non-sensitive metadata.
#   - Claude API key:  apiKeyHelper script, ~5 min cache, no restart needed.
#   - Claude OAuth:    fetched at every container boot, exported to shell.
#                      Picked up on `workstations stop/start`.
#   - GitHub PAT:      git credential.helper, fetched per git operation.
#                      Rotations are picked up immediately, no restart.
#   - Preference:      CLAUDE_CODE_OAUTH_TOKEN (Pro/Max/Team subscription)
#                      over ANTHROPIC_API_KEY (pay-as-you-go).
#
# Idempotent: safe to re-run. Each step checks for existence first.
#
# Usage:
#   PROJECT_ID=my-proj GITHUB_REPO_URL=https://github.com/org/repo.git \
#     ./create-workstation.sh
#
set -euo pipefail

# =============================================================================
# Configuration (override via env vars)
# =============================================================================
PROJECT_ID="${PROJECT_ID:?Set PROJECT_ID}"
REGION="${REGION:-us-central1}"

CLUSTER_NAME="${CLUSTER_NAME:-claude-dev-cluster}"
CONFIG_NAME="${CONFIG_NAME:-claude-dev-config}"
WORKSTATION_NAME="${WORKSTATION_NAME:-claude-dev-$(whoami | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9-')}"

REPO_NAME="${REPO_NAME:-claude-workstation-images}"
IMAGE_NAME="${IMAGE_NAME:-claude-code-workstation}"
IMAGE_TAG="${IMAGE_TAG:-latest}"

SERVICE_ACCOUNT_NAME="${SERVICE_ACCOUNT_NAME:-claude-workstation-sa}"
MACHINE_TYPE="${MACHINE_TYPE:-e2-standard-4}"
DISK_SIZE_GB="${DISK_SIZE_GB:-50}"

GITHUB_REPO_URL="${GITHUB_REPO_URL:?Set GITHUB_REPO_URL (e.g. https://github.com/org/repo.git)}"
GIT_USER_NAME="${GIT_USER_NAME:-$(git config --global user.name 2>/dev/null || echo Developer)}"
GIT_USER_EMAIL="${GIT_USER_EMAIL:-$(git config --global user.email 2>/dev/null || echo dev@example.com)}"

IMAGE_URI="${REGION}-docker.pkg.dev/${PROJECT_ID}/${REPO_NAME}/${IMAGE_NAME}:${IMAGE_TAG}"
SERVICE_ACCOUNT_EMAIL="${SERVICE_ACCOUNT_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"

# =============================================================================
# Helpers
# =============================================================================
log()  { printf '\033[1;34m[%(%H:%M:%S)T]\033[0m %s\n' -1 "$*"; }
warn() { printf '\033[1;33m[%(%H:%M:%S)T] WARN:\033[0m %s\n' -1 "$*"; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"; }

require_cmd gcloud
require_cmd docker
gcloud config set project "${PROJECT_ID}" >/dev/null

# =============================================================================
# 1. Enable APIs
# =============================================================================
log "Enabling required APIs..."
gcloud services enable \
    workstations.googleapis.com \
    artifactregistry.googleapis.com \
    secretmanager.googleapis.com \
    iam.googleapis.com \
    --project="${PROJECT_ID}" --quiet

# =============================================================================
# 2. Artifact Registry repo
# =============================================================================
log "Ensuring Artifact Registry repo '${REPO_NAME}'..."
if ! gcloud artifacts repositories describe "${REPO_NAME}" \
        --location="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    gcloud artifacts repositories create "${REPO_NAME}" \
        --repository-format=docker \
        --location="${REGION}" \
        --description="Claude Code workstation images" \
        --project="${PROJECT_ID}"
fi

# =============================================================================
# 3. Build & push image
# =============================================================================
log "Building image ${IMAGE_URI}..."
gcloud auth configure-docker "${REGION}-docker.pkg.dev" --quiet

# Build for linux/amd64 explicitly (Workstations run amd64; macOS default is arm64)
docker buildx build --platform linux/amd64 -t "${IMAGE_URI}" --push .

# =============================================================================
# 4. Secret Manager
# =============================================================================
secret_exists() {
    gcloud secrets describe "$1" --project="${PROJECT_ID}" >/dev/null 2>&1
}

create_secret_from_stdin() {
    local name="$1"
    gcloud secrets create "${name}" \
        --replication-policy=automatic \
        --data-file=- \
        --project="${PROJECT_ID}"
}

ensure_secret_prompt() {
    local name="$1" prompt="$2"
    if secret_exists "${name}"; then
        log "Secret '${name}' already exists (use 'gcloud secrets versions add' to rotate)"
        return
    fi
    read -rsp "${prompt}: " value; echo
    [[ -z "${value}" ]] && die "Empty value for ${name}"
    printf '%s' "${value}" | create_secret_from_stdin "${name}"
}

# --- Claude auth: prefer OAuth token (Pro/Max/Team), fall back to API key ---
CLAUDE_AUTH_MODE=""   # "oauth" | "apikey"

if secret_exists "claude-code-oauth-token"; then
    log "Found existing 'claude-code-oauth-token' secret — using Claude subscription auth."
    CLAUDE_AUTH_MODE="oauth"
elif secret_exists "anthropic-api-key"; then
    log "Found existing 'anthropic-api-key' secret — using API key auth."
    CLAUDE_AUTH_MODE="apikey"
else
    cat <<'EOF'

Choose Claude Code authentication:
  1) Claude Pro/Max/Team OAuth token  (recommended for subscribers — uses your plan)
  2) Anthropic API key                (pay-as-you-go API billing)

To generate an OAuth token: run `claude setup-token` locally, authorize in
the browser, and copy the printed sk-ant-oat01-... token. Tokens are valid
for ~1 year and can be rotated with `gcloud secrets versions add`.
EOF
    read -rp "Enter choice [1/2]: " choice
    case "${choice}" in
        1)  ensure_secret_prompt "claude-code-oauth-token" \
                "Paste CLAUDE_CODE_OAUTH_TOKEN (sk-ant-oat01-...)"
            CLAUDE_AUTH_MODE="oauth" ;;
        2)  ensure_secret_prompt "anthropic-api-key" "Enter ANTHROPIC_API_KEY"
            CLAUDE_AUTH_MODE="apikey" ;;
        *)  die "Invalid choice: ${choice}" ;;
    esac
fi

ensure_secret_prompt "github-pat" "Enter GitHub PAT (fine-grained, repo-scoped)"

# =============================================================================
# 5. Service account + IAM
# =============================================================================
log "Ensuring service account '${SERVICE_ACCOUNT_EMAIL}'..."
if ! gcloud iam service-accounts describe "${SERVICE_ACCOUNT_EMAIL}" \
        --project="${PROJECT_ID}" >/dev/null 2>&1; then
    gcloud iam service-accounts create "${SERVICE_ACCOUNT_NAME}" \
        --display-name="Claude Workstation SA" \
        --project="${PROJECT_ID}"
fi

if [[ "${CLAUDE_AUTH_MODE}" == "oauth" ]]; then
    CLAUDE_SECRETS=("claude-code-oauth-token")
else
    CLAUDE_SECRETS=("anthropic-api-key")
fi

for secret in "${CLAUDE_SECRETS[@]}" github-pat; do
    gcloud secrets add-iam-policy-binding "${secret}" \
        --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
        --role="roles/secretmanager.secretAccessor" \
        --project="${PROJECT_ID}" --condition=None >/dev/null
done

# Workstation VMs pull the custom container image as this SA at boot.
# Without artifactregistry.reader the VM gets 403 from us-*-docker.pkg.dev
# and the workstation never finishes provisioning ("config is degraded").
# Repo-scoped grant is least-privilege; the SA can only read this one repo.
log "Granting artifactregistry.reader on '${REPO_NAME}'..."
gcloud artifacts repositories add-iam-policy-binding "${REPO_NAME}" \
    --location="${REGION}" \
    --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
    --role="roles/artifactregistry.reader" \
    --project="${PROJECT_ID}" --condition=None >/dev/null

# Container stdout/stderr only flows to Cloud Logging if the workstation SA
# can write logs and metrics. Without these, `gcloud logging read` for the
# workstation returns nothing — making any boot-time failure invisible.
# These are project-scoped because logging/monitoring are project-wide
# services with no resource-level binding for this role.
log "Granting logWriter and metricWriter on project..."
for role in roles/logging.logWriter roles/monitoring.metricWriter; do
    gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
        --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
        --role="${role}" \
        --condition=None >/dev/null
done

# =============================================================================
# 6. Workstations cluster (~20 min first time)
# =============================================================================
log "Ensuring workstations cluster '${CLUSTER_NAME}'..."
if ! gcloud workstations clusters describe "${CLUSTER_NAME}" \
        --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    warn "Creating cluster — this takes ~20 minutes on first run."
    gcloud workstations clusters create "${CLUSTER_NAME}" \
        --region="${REGION}" --project="${PROJECT_ID}"
fi

# =============================================================================
# 7. Workstations config
#    ZERO secrets in the config. The container fetches everything at runtime
#    via the SA + Secret Manager:
#      - Claude API key:  apiKeyHelper script, ~5 min cache, no restart needed
#      - Claude OAuth:    fetched at boot, picked up on workstations start
#      - GitHub PAT:      git credential.helper, fetched per git operation
# =============================================================================
if [[ "${CLAUDE_AUTH_MODE}" == "oauth" ]]; then
    CLAUDE_SECRET_NAME="claude-code-oauth-token"
else
    CLAUDE_SECRET_NAME="anthropic-api-key"
fi

GITHUB_PAT_SECRET_NAME="github-pat"

CONTAINER_ENV="CLAUDE_AUTH_MODE=${CLAUDE_AUTH_MODE}"
CONTAINER_ENV+=",CLAUDE_AUTH_SECRET_NAME=${CLAUDE_SECRET_NAME}"
CONTAINER_ENV+=",GITHUB_PAT_SECRET_NAME=${GITHUB_PAT_SECRET_NAME}"
CONTAINER_ENV+=",GCP_PROJECT_ID=${PROJECT_ID}"
CONTAINER_ENV+=",GITHUB_REPO_URL=${GITHUB_REPO_URL}"
CONTAINER_ENV+=",GIT_USER_NAME=${GIT_USER_NAME}"
CONTAINER_ENV+=",GIT_USER_EMAIL=${GIT_USER_EMAIL}"

log "Auth mode: ${CLAUDE_AUTH_MODE} (Claude secret: ${CLAUDE_SECRET_NAME}, GitHub secret: ${GITHUB_PAT_SECRET_NAME})"

log "Ensuring workstations config '${CONFIG_NAME}'..."
if gcloud workstations configs describe "${CONFIG_NAME}" \
        --cluster="${CLUSTER_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    log "Config exists — updating image & env..."
    gcloud workstations configs update "${CONFIG_NAME}" \
        --cluster="${CLUSTER_NAME}" --region="${REGION}" --project="${PROJECT_ID}" \
        --container-custom-image="${IMAGE_URI}" \
        --container-env="${CONTAINER_ENV}"
else
    gcloud workstations configs create "${CONFIG_NAME}" \
        --cluster="${CLUSTER_NAME}" --region="${REGION}" --project="${PROJECT_ID}" \
        --machine-type="${MACHINE_TYPE}" \
        --pd-disk-size="${DISK_SIZE_GB}" \
        --pd-disk-type="pd-balanced" \
        --container-custom-image="${IMAGE_URI}" \
        --service-account="${SERVICE_ACCOUNT_EMAIL}" \
        --container-env="${CONTAINER_ENV}"
fi

# =============================================================================
# 8. Create + start workstation
# =============================================================================
log "Ensuring workstation '${WORKSTATION_NAME}'..."
if ! gcloud workstations describe "${WORKSTATION_NAME}" \
        --cluster="${CLUSTER_NAME}" --config="${CONFIG_NAME}" \
        --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    gcloud workstations create "${WORKSTATION_NAME}" \
        --cluster="${CLUSTER_NAME}" --config="${CONFIG_NAME}" \
        --region="${REGION}" --project="${PROJECT_ID}"
fi

log "Starting workstation..."
gcloud workstations start "${WORKSTATION_NAME}" \
    --cluster="${CLUSTER_NAME}" --config="${CONFIG_NAME}" \
    --region="${REGION}" --project="${PROJECT_ID}"

# =============================================================================
# 9. Output access info
# =============================================================================
HOSTNAME=$(gcloud workstations describe "${WORKSTATION_NAME}" \
    --cluster="${CLUSTER_NAME}" --config="${CONFIG_NAME}" \
    --region="${REGION}" --project="${PROJECT_ID}" \
    --format="value(host)")

cat <<EOF

================================================================
  Claude Code workstation is ready
================================================================
  Browser IDE  : https://80-${HOSTNAME}
  Workstation  : ${WORKSTATION_NAME}
  Cluster      : ${CLUSTER_NAME}
  Config       : ${CONFIG_NAME}
  Region       : ${REGION}

  Once the IDE loads, open a terminal and run:
      cd ~/workspace
      claude

  SSH to the workstation:
  gcloud workstations ssh ${WORKSTATION_NAME} \\
    --cluster=${CLUSTER_NAME} \\
    --config=claude-dev-config \\
    --region=${REGION} \\
    --project=${PROJECT_ID}

  Stop the workstation (to save cost):
      gcloud workstations stop ${WORKSTATION_NAME} \\
        --cluster=${CLUSTER_NAME} --config=${CONFIG_NAME} \\
        --region=${REGION} --project=${PROJECT_ID}

  Rotate a secret:
      echo -n 'new-value' | gcloud secrets versions add SECRET_NAME \\
        --data-file=- --project=${PROJECT_ID}
      # then re-run this script to pick up the new version
================================================================
EOF
