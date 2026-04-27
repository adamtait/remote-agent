#!/bin/bash
#
# Runs at every workstation boot (as root, inside the container).
#
# Contract (v6):
#   ra create emits these env vars into the container config:
#     GCP_PROJECT_ID, GITHUB_REPO_URL, GIT_USER_NAME, GIT_USER_EMAIL
#     RA_PLUGIN_<NAME>_ENABLED=true|false  — per-plugin toggle
#     RA_PLUGIN_<NAME>_AUTH_PROVIDER=<provider>  — active auth provider
#     RA_SECRETS=<pipe-separated logical names>
#     for each logical <name>:
#       RA_SECRET_<UPPERCASE_NAME>_NAME=<gcp secret name>
#       RA_SECRET_<UPPERCASE_NAME>_VAR=<container env var name>
#     plus any static key=value entries from config.env.
#
# This script does the generic work (fetch secrets, write /run/ra/env, do
# first-boot git setup). Plugins drop their own scripts into
# /etc/workstation-startup.d/ at numbers ≥ 250 — they run independently
# after this one and can source /run/ra/env to access fetched secrets.
#
# Logical names with special handling (still conform to the schema):
#     github_pat     → install git credential helper AND export $VAR for `gh`.
#                      (will move into a github plugin in a follow-up changeset)
#     (any other)    → fetch at boot, export $VAR. Rotation requires restart.
#
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
WORKSPACE_DIR="${USER_HOME}/workspace"

GIT_HELPER_PATH="/usr/local/bin/git-credential-gcp-secret"
RA_SETUP_LOG_PATH="/var/log/ra-setup.log"

RA_RUNTIME_DIR="/run/ra"
RA_ENV_FILE="/run/ra/env"
RA_READY_SENTINEL="/run/ra/ready"

log() {
    local msg="[ra-setup] $(date -u +%H:%M:%S) $*"
    echo "${msg}"
    echo "${msg}" >> "${RA_SETUP_LOG_PATH}" 2>/dev/null || true
}

# _ra_handle_github_pat installs a git credential helper that fetches the
# PAT fresh per git op (so rotation is automatic for git), and also exports
# the PAT to /etc/environment + /run/ra/env so `gh` CLI sees it.
# Will move into a github plugin in a follow-up changeset.
_ra_handle_github_pat() {
    local secret_name="$1"
    local target_env_var="$2"

    log "github_pat: installing git credential helper for ${secret_name}"
    cat > "${GIT_HELPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated. Emits GitHub PAT via git credential protocol.
while IFS= read -r line; do
    [[ -z "\$line" ]] && break
done
pat=\$(gcloud secrets versions access latest \\
    --secret='${secret_name}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || exit 0
echo "username=x-access-token"
echo "password=\${pat}"
EOF
    chmod 0755 "${GIT_HELPER_PATH}"
    git config --system credential.helper "${GIT_HELPER_PATH}"
    log "git credential.helper set to ${GIT_HELPER_PATH}."

    local pat_value
    pat_value=$(gcloud secrets versions access latest \
        --secret="${secret_name}" --project="${GCP_PROJECT_ID}" 2>/dev/null || true)
    if [[ -n "${pat_value}" ]]; then
        echo "${target_env_var}=${pat_value}" >> /etc/environment
        umask 077
        if [[ ! -f "${RA_ENV_FILE}" ]]; then
            : > "${RA_ENV_FILE}"
            chmod 0644 "${RA_ENV_FILE}"
        fi
        sed -i "/^${target_env_var}=/d" "${RA_ENV_FILE}"
        printf '%s=%s\n' "${target_env_var}" "${pat_value}" >> "${RA_ENV_FILE}"
        log "Exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."
    fi
}

# _ra_first_boot_git seeds git identity from container env vars and clones
# the configured repo into the workspace dir. Runs once per workstation;
# guarded by FIRST_BOOT_MARKER.
_ra_first_boot_git() {
    log "First boot: configuring git identity and cloning repo."
    : "${GITHUB_REPO_URL:?GITHUB_REPO_URL not set}"
    : "${GIT_USER_NAME:?GIT_USER_NAME not set}"
    : "${GIT_USER_EMAIL:?GIT_USER_EMAIL not set}"

    sudo -u "${USER_NAME}" git config --global user.name "${GIT_USER_NAME}"
    sudo -u "${USER_NAME}" git config --global user.email "${GIT_USER_EMAIL}"

    if [[ ! -d "${WORKSPACE_DIR}/.git" ]]; then
        sudo -u "${USER_NAME}" mkdir -p "${WORKSPACE_DIR}"
        sudo -u "${USER_NAME}" git clone "${GITHUB_REPO_URL}" "${WORKSPACE_DIR}" \
            2> >(while read -r line; do log "git: ${line}"; done) \
            || log "WARNING: repo clone failed; proceeding anyway."
    fi
    touch "${FIRST_BOOT_MARKER}"
    chown "${USER_NAME}:${USER_NAME}" "${FIRST_BOOT_MARKER}"
}

: > "${RA_SETUP_LOG_PATH}" 2>/dev/null || true
chmod 0644 "${RA_SETUP_LOG_PATH}" 2>/dev/null || true

# =============================================================================
# Required env vars (set by ra create)
# =============================================================================
: "${GCP_PROJECT_ID:?GCP_PROJECT_ID not set}"
: "${RA_SECRETS:?RA_SECRETS not set — check workstations config}"

command -v gcloud >/dev/null 2>&1 || {
    log "ERROR: gcloud CLI missing from image; cannot fetch credentials."
    exit 1
}

# Clear stale auth state common to every boot (git helper, GH_TOKEN line).
rm -f "${GIT_HELPER_PATH}"
sed -i '/^GH_TOKEN=/d' /etc/environment

mkdir -p "${RA_RUNTIME_DIR}"
chmod 0755 "${RA_RUNTIME_DIR}"
rm -f "${RA_READY_SENTINEL}"

# =============================================================================
# Dispatch loop — iterate RA_SECRETS (pipe-separated) and handle each logical name.
# =============================================================================
IFS='|' read -r -a ra_secret_names <<<"${RA_SECRETS}"
for logical in "${ra_secret_names[@]}"; do
    [[ -z "${logical}" ]] && continue
    token=$(echo "${logical}" | tr '[:lower:]' '[:upper:]')
    name_var="RA_SECRET_${token}_NAME"
    env_var_var="RA_SECRET_${token}_VAR"
    secret_name="${!name_var:-}"
    target_env_var="${!env_var_var:-}"

    if [[ -z "${secret_name}" || -z "${target_env_var}" ]]; then
        log "ERROR: secret ${logical} is missing ${name_var} or ${env_var_var}; skipping."
        continue
    fi

    case "${logical}" in
        github_pat)
            _ra_handle_github_pat "${secret_name}" "${target_env_var}"
            ;;

        *)
            log "generic: fetching ${secret_name} → ${target_env_var}"
            val=$(gcloud secrets versions access latest \
                --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
                2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
            if [[ -n "${val}" ]]; then
                echo "${target_env_var}=${val}" >> /etc/environment
                log "Exported ${target_env_var}."
            else
                log "WARNING: ${secret_name} fetch returned empty."
            fi
            ;;
    esac
done

# =============================================================================
# First-boot-only: gitconfig + repo clone.
# =============================================================================
FIRST_BOOT_MARKER="${USER_HOME}/.ra-first-boot-done"
if [[ ! -f "${FIRST_BOOT_MARKER}" ]]; then
    _ra_first_boot_git
fi

touch "${RA_READY_SENTINEL}"
chmod 0644 "${RA_READY_SENTINEL}"
log "Sentinel ${RA_READY_SENTINEL} created; login shells unblocked."

log "Entrypoint complete."
