#!/bin/bash
# claude-sync plugin — image-build hook.
#
# The runtime scripts (270_*.sh and the systemd loop) need `jq`, `curl`,
# `gcloud`, and `systemctl`. All four are present in the Cloud Workstations
# code-oss base image, so this script is a verification + safety-net no-op:
# if a dependency is missing we install it instead of failing at runtime.
set -euo pipefail

need_apt=()

if ! command -v jq >/dev/null 2>&1; then
    need_apt+=(jq)
fi
if ! command -v curl >/dev/null 2>&1; then
    need_apt+=(curl)
fi

if [ "${#need_apt[@]}" -gt 0 ]; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${need_apt[@]}"
    rm -rf /var/lib/apt/lists/*
fi

# gcloud and systemctl come from the base image; we don't try to install them.
command -v gcloud   >/dev/null 2>&1 || echo "[claude-sync] WARN: gcloud not in base image" >&2
command -v systemctl >/dev/null 2>&1 || echo "[claude-sync] WARN: systemctl not in base image" >&2
