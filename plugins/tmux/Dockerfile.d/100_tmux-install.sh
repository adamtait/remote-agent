#!/bin/bash
# tmux plugin — install tmux at image build time.
set -euo pipefail

apt-get update
apt-get install -y --no-install-recommends tmux
rm -rf /var/lib/apt/lists/*
