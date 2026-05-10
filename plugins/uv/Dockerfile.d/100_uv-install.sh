#!/bin/bash
# uv plugin — install uv and uvx at image build time.
set -euo pipefail

if [ -x /usr/local/bin/uv ]; then
    echo "uv already installed, skipping."
    exit 0
fi

curl -LsSf https://astral.sh/uv/install.sh | UV_INSTALL_DIR=/usr/local/bin sh

uv --version
