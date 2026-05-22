#!/bin/bash
# claude-sync plugin — image-build hook.
#
# The runtime scripts need `jq`, `curl`, `gcloud`, and `systemctl`. curl,
# gcloud, and systemctl are guaranteed by the Cloud Workstations code-oss
# base image. jq is NOT — it is not part of the default Debian dev image
# Google ships — so we install it explicitly. We only call apt when jq is
# actually missing, avoiding a redundant apt-get update on every build.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends jq
    rm -rf /var/lib/apt/lists/*
fi

# Soft assertions — fail at runtime is better than blocking the image build.
command -v curl     >/dev/null 2>&1 || echo "[claude-sync] WARN: curl not in base image" >&2
command -v gcloud   >/dev/null 2>&1 || echo "[claude-sync] WARN: gcloud not in base image" >&2
command -v systemctl >/dev/null 2>&1 || echo "[claude-sync] WARN: systemctl not in base image" >&2
