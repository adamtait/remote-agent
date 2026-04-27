#!/bin/bash
# AWS plugin — write ~/.aws/config on every boot.
#
# Credentials (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY) are written to
# /etc/environment by 200_remote-agent-setup.sh via the standard secret
# mechanism. This script only needs to write the non-secret config.
set -euo pipefail

USER_NAME="user"
USER_HOME="/home/${USER_NAME}"
AWS_CONFIG_DIR="${USER_HOME}/.aws"
AWS_CONFIG_FILE="${AWS_CONFIG_DIR}/config"

[ -r /run/ra/env ] && set -a && . /run/ra/env && set +a

[ "${RA_PLUGIN_AWS_ENABLED:-false}" = "true" ] || exit 0

region="${RA_PLUGIN_AWS_REGION:-us-east-1}"
output="${RA_PLUGIN_AWS_OUTPUT:-json}"

# Guard against explicit empty strings (:-default only fires when unset).
[ -n "${region}" ] || region="us-east-1"
[ -n "${output}" ] || output="json"

# Validate against the known-safe value sets.
[[ "${region}" =~ ^[a-z0-9-]+$ ]] || { echo "[aws-configure] invalid region: ${region}" >&2; exit 1; }
[[ "${output}" =~ ^(json|yaml|text|table)$ ]] || { echo "[aws-configure] invalid output: ${output}" >&2; exit 1; }

mkdir -p "${AWS_CONFIG_DIR}"
chown "${USER_NAME}:${USER_NAME}" "${AWS_CONFIG_DIR}"

cat > "${AWS_CONFIG_FILE}" <<EOF
[default]
region = ${region}
output = ${output}
EOF

chmod 600 "${AWS_CONFIG_FILE}"
chown "${USER_NAME}:${USER_NAME}" "${AWS_CONFIG_FILE}"

echo "[aws-configure] wrote ${AWS_CONFIG_FILE} (region=${region}, output=${output})"
