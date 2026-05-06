#!/bin/bash
# tmux plugin — install tmux at image build time.
set -euo pipefail

apt-get update
# ncurses-bin provides infocmp, which 05-tmux-autoattach.sh uses to detect
# unknown-TERM clients (Ghostty, Kitty, …) and fall back to xterm-256color
# before exec'ing tmux. tmux itself only depends on libtinfo6, so we ask for
# ncurses-bin explicitly to keep the fallback robust under base-image churn.
apt-get install -y --no-install-recommends tmux ncurses-bin
rm -rf /var/lib/apt/lists/*

# Force every pane to spawn a login shell. Without this, tmux spawns
# interactive non-login shells, which do not source /etc/profile.d/* — so
# /run/ra/env (sourced by 00-ra-wait.sh) and any plugin secrets exported
# there would be missing in newly-opened windows/splits even though the
# initial attached shell has them.
cat > /etc/tmux.conf <<'EOF'
# ra tmux plugin: ensure new panes are login shells so /etc/profile.d/* runs
# (which sources /run/ra/env), keeping RA_PLUGIN_* and plugin secrets
# available in every window/split, not just the one that exec'd tmux.
set -g default-command "/bin/bash -l"
EOF
chmod 0644 /etc/tmux.conf
