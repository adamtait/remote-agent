#!/bin/bash
# AWS plugin — install the AWS CLI v2 at image build time.
# Always installs the latest release; no version is pinned by design.
set -euo pipefail

if [ -x /usr/local/bin/aws ]; then
    echo "AWS CLI already installed, skipping."
    exit 0
fi

if ! command -v unzip >/dev/null 2>&1; then
    apt-get update -qq
    apt-get install -y --no-install-recommends unzip
    rm -rf /var/lib/apt/lists/*
fi

arch=$(uname -m)
case "${arch}" in
    x86_64)  aws_arch="x86_64" ;;
    aarch64) aws_arch="aarch64" ;;
    *)
        echo "Unsupported architecture: ${arch}" >&2
        exit 1
        ;;
esac

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${aws_arch}.zip" -o "${tmpdir}/awscliv2.zip"
unzip -q "${tmpdir}/awscliv2.zip" -d "${tmpdir}"
"${tmpdir}/aws/install"

aws --version
