#!/bin/bash
# Babashka plugin — install the bb binary at image build time.
# To upgrade: update BB_VERSION (check https://github.com/babashka/babashka/releases).
set -euo pipefail

BB_VERSION="1.12.218"
BB_ARCHIVE="babashka-${BB_VERSION}-linux-amd64-static.tar.gz"
BB_URL="https://github.com/babashka/babashka/releases/download/v${BB_VERSION}/${BB_ARCHIVE}"
BB_DEST="/usr/local/bin/bb"

if [ -x "${BB_DEST}" ] && "${BB_DEST}" --version 2>/dev/null | grep -q "${BB_VERSION}"; then
    echo "babashka ${BB_VERSION} already installed, skipping."
    exit 0
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

curl -fsSL "${BB_URL}" -o "${tmpdir}/${BB_ARCHIVE}"
tar -xzf "${tmpdir}/${BB_ARCHIVE}" -C "${tmpdir}"
install -m 0755 "${tmpdir}/bb" "${BB_DEST}"
bb --version
