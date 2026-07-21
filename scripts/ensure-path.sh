#!/bin/sh
# Offer to add the install dir to the user's PATH by appending an export line
# to their login shell's rc file. Runs from `make install`.
#
# Rules:
#   - If the dir is already on $PATH, do nothing (no prompt).
#   - Never edit config without an interactive "yes".
#   - In non-interactive contexts (no tty, e.g. CI), just print how to do it
#     and exit cleanly — don't prompt, don't hang, don't fail the build.
#   - PATH setup is a convenience, never essential to the install: this script
#     always exits 0 so it can't turn a successful `make install` into a
#     reported failure.
#
# A child process can't mutate the parent shell's live $PATH, so the best we
# can do is update the rc file and tell the user to open a new shell (or
# `source` it). That's the standard approach.
set -u

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

# Pick the rc file for the user's login shell. fish can't source POSIX syntax,
# so we hand it its own command instead of writing an rc file it never reads.
case "$(basename "${SHELL:-sh}")" in
  fish)
    echo "${dir} is not on your PATH. For fish, run:"
    echo "  fish_add_path ${dir}"
    exit 0 ;;
  zsh)
    rc="$HOME/.zshrc" ;;
  bash)
    # macOS bash *login* shells read ~/.bash_profile/~/.profile, not ~/.bashrc;
    # on Linux ~/.bashrc is the right target.
    if [ "$(uname -s)" = "Darwin" ]; then
      if   [ -f "$HOME/.bash_profile" ]; then rc="$HOME/.bash_profile";
      elif [ -f "$HOME/.profile" ];      then rc="$HOME/.profile";
      else rc="$HOME/.bash_profile"; fi
    else
      if   [ -f "$HOME/.bashrc" ];       then rc="$HOME/.bashrc";
      elif [ -f "$HOME/.bash_profile" ]; then rc="$HOME/.bash_profile";
      else rc="$HOME/.profile"; fi
    fi ;;
  *)
    rc="$HOME/.profile" ;;
esac

# Already handled in this rc file (line present but not sourced yet, or added
# by hand in a different form)? Match on the dir path so reformatted entries
# still count — don't duplicate it.
if [ -f "$rc" ] && grep -qsF -- "$dir" "$rc"; then
  echo "${dir} is already configured in ${rc} — run 'source ${rc}' or open a new shell to pick it up."
  exit 0
fi

printf '%s is not on your PATH, so the "ra" command will not be found.\nAdd it to %s? [y/N] ' "$dir" "$rc"
# A bare Ctrl-D (EOF) reads as empty -> treated as "no", never an error.
read -r answer || answer=""
case "$answer" in
  [Yy] | [Yy][Ee][Ss])
    if printf '\n# Added by ra `make install`: put %s on PATH\n%s\n' "$dir" "$line" >> "$rc"; then
      echo "Added to ${rc}. Run 'source ${rc}' (or open a new shell) to use 'ra' now."
    else
      echo "Could not write to ${rc}. Add it yourself with: ${line}"
    fi
    ;;
  *)
    echo "Skipped. Add it yourself later with: ${line}"
    ;;
esac

exit 0
