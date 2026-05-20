# Antigravity plugin — first-run authentication hint.
#
# Shown once per user, then suppressed via a marker in $HOME. Delete the
# marker (~/.ra-antigravity-hint-shown) to see it again. Relies on
# 00-ra-wait.sh having sourced /run/ra/env so RA_PLUGIN_ANTIGRAVITY_ENABLED
# is set before this runs.
#
# agy stores OAuth tokens in the system keyring, which is empty on every
# fresh workstation — so users always need an explicit first-run sign-in
# step. Without this hint that step is invisible.

if [ "${RA_PLUGIN_ANTIGRAVITY_ENABLED:-false}" = "true" ]; then
    _ra_ag_marker="${HOME}/.ra-antigravity-hint-shown"
    if [ ! -e "${_ra_ag_marker}" ] && [ -t 1 ]; then
        echo ""
        echo "→ Antigravity CLI (agy) is installed but not yet authenticated."
        echo "  Run 'agy' to start the OAuth flow — it will print a URL to"
        echo "  open in a browser on your local machine."
        echo ""
        touch "${_ra_ag_marker}" 2>/dev/null || true
    fi
    unset _ra_ag_marker
fi
