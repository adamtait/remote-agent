#!/bin/bash
#
# Runs at every workstation boot (as root, inside the container).
#
# Contract (v6):
#   ra create emits these env vars into the container config:
#     GCP_PROJECT_ID
#     RA_PLUGIN_<NAME>_ENABLED=true|false  — per-plugin toggle
#     RA_PLUGIN_<NAME>_AUTH_PROVIDER=<provider>  — active auth provider
#     RA_SECRETS=<pipe-separated logical names>
#     for each logical <name>:
#       RA_SECRET_<UPPERCASE_NAME>_NAME=<gcp secret name>
#       RA_SECRET_<UPPERCASE_NAME>_VAR=<container env var name>
#     plus any static key=value entries from config.env.
#
# This script does the generic work (fetch secrets, write /run/ra/env).
# Plugins drop their own scripts into /etc/workstation-startup.d/ at
# numbers ≥ 250 — they run independently after this one and can source
# /run/ra/env to access fetched secrets.
#
# All logical names: fetch at boot, export $VAR. Rotation requires restart.
# Plugins that need per-op rotation (e.g. github) install their own credential
# helpers in workstation-startup.d/ at numbers ≥ 250.
#
set -euo pipefail

RA_SETUP_LOG_PATH="/var/log/ra-setup.log"

RA_RUNTIME_DIR="/run/ra"
RA_ENV_FILE="/run/ra/env"
RA_READY_SENTINEL="/run/ra/ready"

log() {
    local msg="[ra-setup] $(date -u +%H:%M:%S) $*"
    echo "${msg}"
    echo "${msg}" >> "${RA_SETUP_LOG_PATH}" 2>/dev/null || true
}

# Install the shared ra-env helper library, then source it. Plugin scripts
# (workstation-startup.d/ at numbers >= 250) run as SEPARATE processes and
# source this same file, so env-var persistence — per-key dedup, the dual write
# to /etc/environment and /run/ra/env, and the single-quote escaping — lives in
# exactly one place. Emitted with a single-quoted heredoc (<<'RA_ENV_LIB') so
# the escaping expressions below are written to the file literally, not expanded
# here. This script sources its own emitted lib so there is no duplicated body.
install -d -m 0755 /usr/local/lib/ra
cat > /usr/local/lib/ra/ra-env.sh <<'RA_ENV_LIB'
#!/bin/bash
# ra-env helper library — installed by 200_remote-agent-setup.sh; sourced by the
# core setup script and by plugin workstation-startup.d scripts.
#
#   ra_env_set KEY VALUE  — dedup + write KEY to BOTH /etc/environment and
#                           /run/ra/env, single-quote escaped. Creates
#                           /run/ra/env lazily, owned user:user 0600.
#   ra_env_unset KEY      — remove KEY from BOTH files. Idempotent; set -e safe.
#
# Both files are single-quote-wrapped (with embedded-quote escaping `'\''`) so a
# value containing spaces or shell metacharacters can't be re-parsed as extra
# commands when the file is sourced:
#   - /run/ra/env is bash-sourced via `set -a; . FILE`.
#   - /etc/environment is read by pam_env as plain KEY=VAL, BUT some system tools
#     also `. /etc/environment` (e.g. Debian's /usr/sbin/update-info-dir, run by
#     the install-info dpkg trigger under `set -e`). An unquoted value with a `|`
#     — like RA_PLUGIN_GITHUB_REPO_URLS's pipe-separated list — would otherwise be
#     executed as a pipeline, failing the trigger and breaking apt for any package
#     that ships info files (emacs, etc.). pam_env strips the surrounding quotes,
#     so login shells still receive the clean value.
#
# Keys are valid env var names ([A-Za-z_][A-Za-z0-9_]*), so the sed regexes are
# safe without escaping. Plugins don't set RA_ENV_FILE, so default it here.
: "${RA_ENV_FILE:=/run/ra/env}"

ra_env_set() {
    local key="$1"
    local val="$2"
    local quoted="${val//\'/\'\\\'\'}"

    [ -f /etc/environment ] || : > /etc/environment
    sed -i "/^${key}=/d" /etc/environment
    printf "%s='%s'\n" "${key}" "${quoted}" >> /etc/environment

    if [ ! -f "${RA_ENV_FILE}" ]; then
        # Create 0600 from the start. The umask is scoped to a subshell so it
        # can't leak into a sourcing plugin's shell and tighten files that
        # plugin creates afterward. Holds fetched secret values (OAuth tokens,
        # API keys); the `user` login shell sources it via
        # profile.d/00-ra-wait.sh, so own it by `user` (root, the writer, can
        # read it regardless).
        ( umask 077; : > "${RA_ENV_FILE}" )
        chown user:user "${RA_ENV_FILE}"
        chmod 0600 "${RA_ENV_FILE}"
    fi
    sed -i "/^${key}=/d" "${RA_ENV_FILE}"
    printf "%s='%s'\n" "${key}" "${quoted}" >> "${RA_ENV_FILE}"
}

ra_env_unset() {
    local key="$1"
    [ -f /etc/environment ] && sed -i "/^${key}=/d" /etc/environment
    [ -f "${RA_ENV_FILE}" ] && sed -i "/^${key}=/d" "${RA_ENV_FILE}"
    return 0
}
RA_ENV_LIB
chmod 0644 /usr/local/lib/ra/ra-env.sh
# shellcheck source=/dev/null
. /usr/local/lib/ra/ra-env.sh


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

    log "fetching ${secret_name} → ${target_env_var}"
    val=$(gcloud secrets versions access latest \
        --secret="${secret_name}" --project="${GCP_PROJECT_ID}" \
        2> >(while read -r line; do log "gcloud: ${line}"; done) || true)
    if [[ -n "${val}" ]]; then
        ra_env_set "${target_env_var}" "${val}"
        log "Exported ${target_env_var} via /etc/environment and ${RA_ENV_FILE}."
    else
        log "WARNING: ${secret_name} fetch returned empty."
    fi
done

# =============================================================================
# Propagate container env to login shells.
#
# `--container-env=` from `gcloud workstations configs ...` lands in the
# container's PID-1 environment, which startup-d scripts inherit but sshd's
# login shells do not. Without this loop, RA_PLUGIN_<NAME>_ENABLED (and other
# plugin/identity/free-form config vars) are visible to
# /etc/workstation-startup.d/ scripts but missing from /etc/environment and
# /run/ra/env — so /etc/profile.d/ gates like the tmux autoattach silently
# default to disabled.
#
# RA_PROPAGATE_KEYS is a pipe-separated allowlist emitted by `ra create`
# (cmd/create.go buildContainerEnv). Iterating it — rather than walking
# `env` with a prefix denylist — keeps RA_SECRETS bookkeeping out and
# handles values containing newlines correctly (we look up via ${!key}
# instead of parsing line-oriented `env` output).
# =============================================================================
if [[ -n "${RA_PROPAGATE_KEYS:-}" ]]; then
    IFS='|' read -r -a _ra_propagate_keys <<<"${RA_PROPAGATE_KEYS}"
    for _key in "${_ra_propagate_keys[@]}"; do
        [[ -z "${_key}" ]] && continue
        _val="${!_key:-}"
        ra_env_set "${_key}" "${_val}"
        log "Propagated ${_key} via /etc/environment and ${RA_ENV_FILE}."
    done
    unset _ra_propagate_keys _key _val
fi


touch "${RA_READY_SENTINEL}"
chmod 0644 "${RA_READY_SENTINEL}"
log "Sentinel ${RA_READY_SENTINEL} created; login shells unblocked."

log "Entrypoint complete."
