#!/bin/sh
# Offer to add the install dir to the user's PATH by appending an export line
# to their login shell's rc file. Runs from `make install`.
#
# Rules:
#   - If the dir is already on $PATH, do nothing (no prompt).
#   - Never edit config without an interactive "yes".
#   - In non-interactive contexts (no tty, e.g. CI), just print how to do it
#     and exit cleanly — don't prompt, don't hang, don't fail the build.
#
# A child process can't mutate the parent shell's live $PATH, so the best we
# can do is update the rc file and tell the user to open a new shell (or
# `source` it). That's the standard approach.
set -eu

dir="${1:-$HOME/.local/bin}"

# Already on PATH? Nothing to do.
case ":${PATH}:" in
  *":${dir}:"*) exit 0 ;;
esac

line="export PATH=\"${dir}:\$PATH\""

# Non-interactive (no controlling tty): guide, but never edit files or block.
if [ ! -t 0 ]; then
  echo "Note: ${dir} is not on your PATH — the 'ra' command won't be found."
  echo "Add it with: ${line}"
  exit 0
fi

# Pick the rc file for the user's login shell.
case "$(basename "${SHELL:-sh}")" in
  zsh)  rc="$HOME/.zshrc" ;;
  bash)
    if   [ -f "$HOME/.bashrc" ];       then rc="$HOME/.bashrc";
    elif [ -f "$HOME/.bash_profile" ]; then rc="$HOME/.bash_profile";
    else rc="$HOME/.profile"; fi ;;
  *)    rc="$HOME/.profile" ;;
esac

# Already written to the rc file (just not sourced yet)? Don't duplicate it.
if [ -f "$rc" ] && grep -qsF "$line" "$rc"; then
  echo "${dir} is already configured in ${rc} — run 'source ${rc}' or open a new shell to pick it up."
  exit 0
fi

printf '%s is not on your PATH, so the "ra" command will not be found.\nAdd it to %s? [y/N] ' "$dir" "$rc"
read -r answer
case "$answer" in
  [Yy] | [Yy][Ee][Ss])
    printf '\n# Added by ra `make install`: put %s on PATH\n%s\n' "$dir" "$line" >> "$rc"
    echo "Added to ${rc}. Run 'source ${rc}' (or open a new shell) to use 'ra' now."
    ;;
  *)
    echo "Skipped. Add it yourself later with: ${line}"
    ;;
esac
