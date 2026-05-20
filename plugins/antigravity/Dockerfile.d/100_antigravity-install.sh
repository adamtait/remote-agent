#!/bin/bash
# Antigravity plugin — install the agy CLI at image build time.
#
# Uses Google's official bootstrap script, which downloads the platform
# binary, verifies its SHA512, and drops it into the target dir. We pass
# --dir /usr/local/bin so the binary lands on PATH for every user.
#
# agy self-updates in the background at runtime, so pinning a version
# here is unnecessary. Authentication is OAuth-based with system-keyring
# storage (or out-of-band URL flow over SSH) and must be completed
# interactively by the user on first run.
set -euo pipefail

if [ -x /usr/local/bin/agy ]; then
    echo "agy already installed, skipping."
    exit 0
fi

curl -fsSL https://antigravity.google/cli/install.sh \
    | bash -s -- --dir /usr/local/bin

agy --version 2>/dev/null || true
