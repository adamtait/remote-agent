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
        curl ca-certificates gnupg git git-lfs jq unzip \
    && git lfs install --system \
    && rm -rf /var/lib/apt/lists/*

RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)

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
