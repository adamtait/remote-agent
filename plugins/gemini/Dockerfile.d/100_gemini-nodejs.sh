#!/bin/bash
# Gemini plugin — install Node.js 20 at image build time.
# Required because the Gemini CLI is distributed as an npm package.
# Idempotent: NodeSource setup script + apt re-install is safe to run twice
# if another plugin (e.g. claude) has already done it.
set -euo pipefail

curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y --no-install-recommends nodejs
rm -rf /var/lib/apt/lists/*
