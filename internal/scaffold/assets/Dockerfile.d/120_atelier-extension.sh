#!/usr/bin/env bash
set -euo pipefail

ATELIER_VERSION="${ATELIER_VERSION:-0.1.2}"
ATELIER_VSIX_URL="${ATELIER_VSIX_URL:-https://atelier.dev/atelier-${ATELIER_VERSION}.vsix}"
ATELIER_EXTENSION_DIR="/opt/code-oss/extensions/atelier"

tmp="$(mktemp -d)"
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT

curl -fsSL "$ATELIER_VSIX_URL" -o "$tmp/atelier.vsix"
cd "$tmp"
unzip -q atelier.vsix "extension/*"
test -f extension/package.json

mkdir -p "$(dirname "$ATELIER_EXTENSION_DIR")"
rm -rf "$ATELIER_EXTENSION_DIR"
mv extension "$ATELIER_EXTENSION_DIR"
chmod -R a+rX "$ATELIER_EXTENSION_DIR"
