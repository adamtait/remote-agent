#!/bin/bash
# Claude plugin — install Node.js 20 at image build time.
# Required because Claude Code is distributed as an npm package.
set -euo pipefail

curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y --no-install-recommends nodejs
rm -rf /var/lib/apt/lists/*
