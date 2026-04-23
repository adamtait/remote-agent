# Custom Cloud Workstations image:
#   base code-oss IDE  +  Node.js 20  +  git/git-lfs  +  gh CLI  +  Claude Code
#
# The base image runs every executable in /etc/workstation-startup.d/ at boot;
# we drop our setup script in there.
FROM us-central1-docker.pkg.dev/cloud-workstations-images/predefined/code-oss:latest

USER root

# --- System packages --------------------------------------------------------
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates gnupg git git-lfs jq \
    && git lfs install --system \
    && rm -rf /var/lib/apt/lists/*

# --- Node.js 20 -------------------------------------------------------------
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# --- GitHub CLI -------------------------------------------------------------
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# --- Claude Code ------------------------------------------------------------
RUN npm install -g @anthropic-ai/claude-code \
    && npm cache clean --force

# --- Sanity check: entrypoint relies on gcloud being present for runtime
#     Secret Manager access. The code-oss base image ships with gcloud, but
#     fail the build loudly if that ever changes.
RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)

# --- Our startup script -----------------------------------------------------
# Numbered 200 so it runs after the base image's own 100-series scripts.
COPY .ws/entrypoint.sh /etc/workstation-startup.d/200_claude-setup.sh
RUN chmod +x /etc/workstation-startup.d/200_claude-setup.sh

# --- Login-shell profile.d scripts ------------------------------------------
# `ws create` stages .ws/profile.d/ and ~/.config/ws/profile.d/ into
# .ws/profile.d.staged/ just before invoking docker buildx (project
# overrides user-global on filename collision). The staging helper chmods each
# .sh to 0755 before COPY, so no RUN chmod is needed. In other build flows
# (e.g. manual `docker build`) the caller is responsible for populating
# .ws/profile.d.staged/ with executable scripts.
COPY .ws/profile.d.staged/ /etc/profile.d/

# The base image's ENTRYPOINT handles IDE startup; don't override it.
