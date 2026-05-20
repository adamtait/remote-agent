#!/bin/bash
# Antigravity plugin — install the agy CLI at image build time.
#
# Uses Google's official bootstrap script, which downloads the platform
# binary, verifies its SHA512, and drops it into the target dir. We pass
# --dir /usr/local/bin so the binary lands on PATH for every user.
#
# Trust model: the bootstrap script itself is fetched fresh on every
# build (no version pin), but it SHA512-verifies the binary it
# downloads. If Google's bootstrap URL is ever compromised, both the
# script and the manifest checksum could be swapped together; we accept
# that risk in exchange for not having to track agy releases.
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

agy --version
