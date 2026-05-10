#!/bin/bash
# uv plugin — install uv and uvx at image build time.
set -euo pipefail

if [ -x /usr/local/bin/uv ]; then
    echo "uv already installed, skipping."
    exit 0
fi

UV_VERSION=$(curl -fsSL https://api.github.com/repos/astral-sh/uv/releases/latest \
    | grep '"tag_name"' | head -1 | sed 's/.*"\([^"]*\)".*/\1/')

arch=$(uname -m)
case "${arch}" in
    x86_64)  uv_arch="x86_64-unknown-linux-gnu" ;;
    aarch64) uv_arch="aarch64-unknown-linux-gnu" ;;
    *)
        echo "Unsupported architecture: ${arch}" >&2
        exit 1
        ;;
esac

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

curl -fsSL "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/uv-${uv_arch}.tar.gz" \
    | tar -xz -C "${tmpdir}"
install -m 0755 "${tmpdir}/uv-${uv_arch}/uv"  /usr/local/bin/uv
install -m 0755 "${tmpdir}/uv-${uv_arch}/uvx" /usr/local/bin/uvx

uv --version
