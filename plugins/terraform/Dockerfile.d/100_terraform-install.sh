#!/bin/bash
# Terraform plugin — install the latest terraform binary at image build time.
set -euo pipefail

TF_DEST="/usr/local/bin/terraform"

# HashiCorp's checkpoint API avoids the unauthenticated GitHub API rate limit.
TF_VERSION=$(curl -fsSL https://checkpoint-api.hashicorp.com/v1/check/terraform \
    | grep -o '"current_version":"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')

if [ -z "${TF_VERSION}" ]; then
    echo "Failed to determine latest terraform version" >&2
    exit 1
fi

if [ -x "${TF_DEST}" ] && "${TF_DEST}" version 2>/dev/null | grep -q "v${TF_VERSION}"; then
    echo "terraform ${TF_VERSION} already installed, skipping."
    exit 0
fi

if ! command -v unzip >/dev/null 2>&1; then
    apt-get update -qq
    apt-get install -y --no-install-recommends unzip
    rm -rf /var/lib/apt/lists/*
fi

arch=$(uname -m)
case "${arch}" in
    x86_64)  tf_arch="amd64" ;;
    aarch64) tf_arch="arm64" ;;
    *)
        echo "Unsupported architecture: ${arch}" >&2
        exit 1
        ;;
esac

TF_ARCHIVE="terraform_${TF_VERSION}_linux_${tf_arch}.zip"
TF_BASE="https://releases.hashicorp.com/terraform/${TF_VERSION}"
TF_URL="${TF_BASE}/${TF_ARCHIVE}"
TF_SUMS_URL="${TF_BASE}/terraform_${TF_VERSION}_SHA256SUMS"

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

curl -fsSL "${TF_URL}" -o "${tmpdir}/${TF_ARCHIVE}"
curl -fsSL "${TF_SUMS_URL}" -o "${tmpdir}/SHA256SUMS"

# Verify the downloaded archive against HashiCorp's published checksum.
# Filter SHA256SUMS to just our archive so sha256sum -c doesn't choke on
# files that aren't present locally.
expected_line=$(grep " ${TF_ARCHIVE}\$" "${tmpdir}/SHA256SUMS" || true)
if [ -z "${expected_line}" ]; then
    echo "No checksum entry for ${TF_ARCHIVE} in SHA256SUMS" >&2
    exit 1
fi
(cd "${tmpdir}" && echo "${expected_line}" | sha256sum -c -)

unzip -q "${tmpdir}/${TF_ARCHIVE}" -d "${tmpdir}"
install -m 0755 "${tmpdir}/terraform" "${TF_DEST}"
terraform version
