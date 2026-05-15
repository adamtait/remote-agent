#!/bin/bash
# marimo plugin — install marimo (reactive Python notebook) at image build time.
#
# Strategy: vendor a uv install (no-op if /usr/local/bin/uv already exists, so
# this coexists with the uv plugin) and use `uv tool install marimo` to land a
# `marimo` shim on system PATH backed by an isolated venv under /opt/uv-tools.
# This avoids polluting any system Python and works whether or not the user
# installed the uv plugin separately.
set -euo pipefail

# --- Phase A: ensure uv is present ----------------------------------------
if [ ! -x /usr/local/bin/uv ]; then
    UV_VERSION=$(curl -fsSL https://api.github.com/repos/astral-sh/uv/releases/latest \
        | grep '"tag_name"' | head -1 | sed 's/.*"\([^"]*\)".*/\1/')

    arch=$(uname -m)
    case "${arch}" in
        x86_64)  uv_arch="x86_64-unknown-linux-gnu" ;;
        aarch64) uv_arch="aarch64-unknown-linux-gnu" ;;
        *)
            echo "Unsupported architecture: ${arch}" >&2
            exit 1
            ;;
    esac

    tmpdir=$(mktemp -d)
    trap 'rm -rf "${tmpdir}"' EXIT

    UV_ARCHIVE="uv-${uv_arch}.tar.gz"
    UV_BASE="https://github.com/astral-sh/uv/releases/download/${UV_VERSION}"

    curl -fsSL "${UV_BASE}/${UV_ARCHIVE}"        -o "${tmpdir}/${UV_ARCHIVE}"
    curl -fsSL "${UV_BASE}/${UV_ARCHIVE}.sha256" -o "${tmpdir}/${UV_ARCHIVE}.sha256"

    # Verify the downloaded tarball against the upstream-published checksum
    # (file format is the standard `<hash>  <filename>` accepted by `sha256sum -c`).
    (cd "${tmpdir}" && sha256sum -c "${UV_ARCHIVE}.sha256")

    tar -xz -C "${tmpdir}" -f "${tmpdir}/${UV_ARCHIVE}"
    install -m 0755 "${tmpdir}/uv-${uv_arch}/uv"  /usr/local/bin/uv
    install -m 0755 "${tmpdir}/uv-${uv_arch}/uvx" /usr/local/bin/uvx
fi

uv --version

# --- Phase B: install marimo system-wide ----------------------------------
# UV_TOOL_DIR  — where uv stores the tool venv (default ~/.local/share/uv/tools
#                lands under /root, which would be unreadable to the workstation
#                user). Pinning to /opt keeps it on a system path.
# UV_TOOL_BIN_DIR — where uv writes the launcher shim. /usr/local/bin puts
#                `marimo` on every user's PATH.
# UV_PYTHON_INSTALL_DIR — where uv downloads managed Python interpreters. Same
#                rationale: stable system path, world-readable.
export UV_TOOL_DIR=/opt/uv-tools
export UV_TOOL_BIN_DIR=/usr/local/bin
export UV_PYTHON_INSTALL_DIR=/opt/uv-python

# Pre-install Python deterministically so `uv tool install marimo` doesn't
# trigger an opportunistic interpreter download on first boot.
uv python install 3.12

uv tool install marimo

# The default umask on `uv tool install` can leave parts of the tool/Python
# trees mode 0700 (root-only). Make them world-readable so the shim and its
# venv work when invoked as the workstation user.
chmod -R a+rX /opt/uv-tools /opt/uv-python

marimo --version
