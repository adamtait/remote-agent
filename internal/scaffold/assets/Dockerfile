# ra workstation image — generic.
#
# Build context root is .ra/.build/, assembled fresh by `ra create` via
# scaffold.AssembleBuildContext. All COPY paths are relative to that root.
# This file ships with no plugin-specific content; plugins drop scripts
# into Dockerfile.d/, workstation-startup.d/, and profile.d/ (those dirs
# are merged from project + user + plugin sources at assembly time).
FROM us-central1-docker.pkg.dev/cloud-workstations-images/predefined/code-oss:latest

USER root

# --- Core system packages ---------------------------------------------------
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates gnupg git git-lfs jq \
    && git lfs install --system \
    && rm -rf /var/lib/apt/lists/*

RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)

# --- SSH keepalive ----------------------------------------------------------
# `gcloud workstations ssh` tunnels SSH over a websocket and provides no
# keepalive on either layer: it hard-codes the client-side
# `-o ServerAliveInterval=0` (which wins, since ssh honors the first -o value,
# so a client --ssh-flag can't override it) and opens the websocket with no
# ping. An idle session's tunnel is therefore closed by the backend, surfacing
# as "ERROR: Connection to Cloud Workstation lost." Drive keepalives from the
# server instead: sshd sends ClientAlive probes over the tunnel, keeping the
# websocket warm.
#
# Interval must be comfortably *under* the backend's idle-reap window, which
# measures ~30s empirically. The original 30s value was too long — its first
# probe fires *at* ~30s and loses the race to the reap, so idle sessions still
# dropped at ~30-35s. 15s gives a ~2x margin: verified on a live workstation,
# an idle session that drops at ~30s under a 30s interval survives 90s+ under
# 15s. ClientAliveCountMax bounds how long an unresponsive client is tolerated
# (15s x 10 = ~2.5 min) before sshd reaps it.
# Debian/Ubuntu images Include sshd_config.d/*.conf at the top of sshd_config
# (first-wins), so this drop-in takes effect; prepend the Include if missing.
RUN mkdir -p /etc/ssh/sshd_config.d \
    && printf 'ClientAliveInterval 15\nClientAliveCountMax 10\n' \
        > /etc/ssh/sshd_config.d/10-ra-keepalive.conf \
    && if ! grep -qE '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config; then \
        printf 'Include /etc/ssh/sshd_config.d/*.conf\n' | cat - /etc/ssh/sshd_config > /etc/ssh/sshd_config.ra \
        && mv /etc/ssh/sshd_config.ra /etc/ssh/sshd_config; \
    fi

# --- Plugin build-time hooks ------------------------------------------------
COPY Dockerfile.d/ /tmp/ra-dockerfile-d/
RUN set -e; \
    for s in $(find /tmp/ra-dockerfile-d -name '*.sh' -type f | sort); do \
        echo "==> running build hook: $s"; \
        bash "$s"; \
    done; \
    rm -rf /tmp/ra-dockerfile-d

# --- Plugin tree (informational; live paths are below) ----------------------
COPY plugins/ /etc/ra/plugins/
RUN find /etc/ra/plugins -type f -name '*.sh' -exec chmod 0755 {} +

# --- Workstation-startup scripts (merged: ra core + plugins) ----------------
COPY workstation-startup.d/ /etc/workstation-startup.d/
RUN chmod 0755 /etc/workstation-startup.d/*.sh 2>/dev/null || true

# --- Login-shell profile.d scripts (merged: ra core + plugins) --------------
COPY profile.d/ /etc/profile.d/
RUN chmod 0644 /etc/profile.d/*.sh 2>/dev/null || true
