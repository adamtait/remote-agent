#!/bin/bash
# Terraform plugin — install the latest terraform binary at image build time.
set -euo pipefail

TF_DEST="/usr/local/bin/terraform"

TF_VERSION=$(curl -fsSL https://api.github.com/repos/hashicorp/terraform/releases/latest \
    | grep '"tag_name"' | head -1 | sed 's/.*"v\([^"]*\)".*/\1/')

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
TF_URL="https://releases.hashicorp.com/terraform/${TF_VERSION}/${TF_ARCHIVE}"

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

curl -fsSL "${TF_URL}" -o "${tmpdir}/${TF_ARCHIVE}"
unzip -q "${tmpdir}/${TF_ARCHIVE}" -d "${tmpdir}"
install -m 0755 "${tmpdir}/terraform" "${TF_DEST}"
terraform version
