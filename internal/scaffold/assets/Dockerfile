# Custom Cloud Workstations image:
#   base code-oss IDE  +  Node.js 20  +  git/git-lfs  +  gh CLI  (+ optional harnesses)
#
# The base image runs every executable in /etc/workstation-startup.d/ at boot;
# we drop our setup script in there. Per-harness setup (e.g. Claude Code) is
# opt-in via build args and sourced at runtime.
FROM us-central1-docker.pkg.dev/cloud-workstations-images/predefined/code-oss:latest

# Build-time harness toggles. `ws create` passes these from cfg.Claude.IsEnabled().
ARG WS_CLAUDE_ENABLED=false

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

# --- Sanity check: entrypoint relies on gcloud being present for runtime
#     Secret Manager access. The code-oss base image ships with gcloud, but
#     fail the build loudly if that ever changes.
RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)

# --- Optional: Claude Code harness -----------------------------------------
# install.sh runs as root at build time, but only when WS_CLAUDE_ENABLED=true.
# It npm-installs @anthropic-ai/claude-code as the unprivileged 'user' so the
# package lives in /home/user/.npm-global (required for auto-update).
COPY .ws/claude/install.sh /tmp/ws-claude-install.sh
RUN if [ "$WS_CLAUDE_ENABLED" = "true" ]; then \
        bash /tmp/ws-claude-install.sh; \
    fi \
    && rm -f /tmp/ws-claude-install.sh

# setup.sh is always copied into the image; the runtime entrypoint sources it
# iff WS_CLAUDE_ENABLED=true (via container env). Keeping setup.sh present in
# both enabled and disabled builds means flipping the flag doesn't need an
# image rebuild if the harness is simply switched off.
RUN mkdir -p /etc/ws
COPY .ws/claude/setup.sh /etc/ws/claude-setup.sh
RUN chmod 0755 /etc/ws/claude-setup.sh

# Kept unconditionally. In Claude-disabled builds /home/user/.npm-global does
# not exist; PATH entries for nonexistent dirs are harmless.
ENV PATH="/home/user/.npm-global/bin:${PATH}"

# --- Our startup script -----------------------------------------------------
# Numbered 200 so it runs after the base image's own 100-series scripts.
COPY .ws/entrypoint.sh /etc/workstation-startup.d/200_ws-setup.sh
RUN chmod +x /etc/workstation-startup.d/200_ws-setup.sh

# --- Login-shell profile.d scripts ------------------------------------------
# `ws create` stages .ws/profile.d/ and ~/.config/ws/profile.d/ into
# .ws/profile.d.staged/ just before invoking docker buildx (project
# overrides user-global on filename collision). The staging helper chmods each
# .sh to 0755 before COPY, so no RUN chmod is needed. In other build flows
# (e.g. manual `docker build`) the caller is responsible for populating
# .ws/profile.d.staged/ with executable scripts.
COPY .ws/profile.d.staged/ /etc/profile.d/

# The base image's ENTRYPOINT handles IDE startup; don't override it.
