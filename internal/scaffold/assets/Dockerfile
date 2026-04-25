# Custom Cloud Workstations image:
#   base code-oss IDE  +  Node.js 20  +  git/git-lfs  +  gh CLI  (+ optional harnesses)
#
# The base image runs every executable in /etc/workstation-startup.d/ at boot;
# we drop our setup script in there. Per-harness setup (e.g. Claude Code) is
# opt-in: harness scripts are always baked into the image and gated at runtime
# by container env vars (e.g. RA_CLAUDE_ENABLED). Harness install steps run
# at first-boot rather than at docker build, because the base image creates
# the unprivileged 'user' account via runtime startup hooks — it does not
# exist in /etc/passwd at build time, so build-time `chown user:user` fails.
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

# --- Sanity check: entrypoint relies on gcloud being present for runtime
#     Secret Manager access. The code-oss base image ships with gcloud, but
#     fail the build loudly if that ever changes.
RUN command -v gcloud >/dev/null || (echo "gcloud missing from base image" && exit 1)

# --- Optional: Claude Code harness -----------------------------------------
# Both scripts are always baked into the image; the runtime entrypoint sources
# claude-setup.sh iff RA_CLAUDE_ENABLED=true (via container env). On first
# boot, claude-setup.sh shells out to claude-install.sh to npm-install
# @anthropic-ai/claude-code under /home/user/.npm-global so the package can
# self-update under the 'user' account.
RUN mkdir -p /etc/ra
COPY .ra/claude/install.sh /etc/ra/claude-install.sh
COPY .ra/claude/setup.sh /etc/ra/claude-setup.sh
RUN chmod 0755 /etc/ra/claude-install.sh /etc/ra/claude-setup.sh

# Make /home/user/.npm-global/bin discoverable by login shells (sshd / IDE
# terminal) and by `sudo -u user` invocations. A Dockerfile `ENV PATH=` only
# reaches processes the container engine spawns from this image's entrypoint;
# Cloud Workstations interactive shells build PATH from /etc/profile.d/, and
# `sudo` strips the inherited PATH per its secure_path setting. Kept
# unconditionally — if Claude is disabled the directory never exists, but a
# missing PATH entry is harmless.
RUN printf 'export PATH="/home/user/.npm-global/bin:$PATH"\n' \
        > /etc/profile.d/00-ra-npm-global-path.sh \
    && chmod 0644 /etc/profile.d/00-ra-npm-global-path.sh

# --- Our startup script -----------------------------------------------------
# Numbered 200 so it runs after the base image's own 100-series scripts.
COPY .ra/entrypoint.sh /etc/workstation-startup.d/200_ra-setup.sh
RUN chmod +x /etc/workstation-startup.d/200_ra-setup.sh

# --- Login-shell profile.d scripts ------------------------------------------
# `ra create` stages .ra/profile.d/ and ~/.config/ra/profile.d/ into
# .ra/profile.d.staged/ just before invoking docker buildx (project
# overrides user-global on filename collision). The staging helper chmods each
# .sh to 0755 before COPY, so no RUN chmod is needed. In other build flows
# (e.g. manual `docker build`) the caller is responsible for populating
# .ra/profile.d.staged/ with executable scripts.
COPY .ra/profile.d.staged/ /etc/profile.d/

# The base image's ENTRYPOINT handles IDE startup; don't override it.
