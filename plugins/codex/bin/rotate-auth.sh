#!/bin/bash
# Codex plugin — rotate the OAuth auth.json secret in GCP Secret Manager.
#
# Run from your laptop after re-running `codex login` locally to refresh
# an expired ChatGPT token. Adds a new version to the GCP secret
# referenced by your ra config; restart the workstation to pick it up.
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: rotate-auth.sh [<auth-json-path>] [--secret <name>] [--project <id>]

Updates the GCP Secret Manager secret referenced by `secrets.codex_auth_json`
in your ra config with the base64-encoded contents of <auth-json-path>.

Defaults:
  <auth-json-path>  $HOME/.codex/auth.json
  --secret          looked up from ./.ra/config.yaml or ~/.config/ra/config.yaml
  --project         `gcloud config get-value project`
USAGE
}

AUTH_JSON=""
SECRET=""
PROJECT=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --secret)  SECRET="$2";  shift 2;;
        --project) PROJECT="$2"; shift 2;;
        -h|--help) usage; exit 0;;
        --*)       echo "unknown flag: $1" >&2; usage >&2; exit 2;;
        *)         AUTH_JSON="$1"; shift;;
    esac
done

AUTH_JSON="${AUTH_JSON:-$HOME/.codex/auth.json}"

if [[ ! -r "$AUTH_JSON" ]]; then
    echo "auth.json not found or unreadable: $AUTH_JSON" >&2
    echo "Run \`codex login\` first to generate it." >&2
    exit 1
fi

_lookup_secret_name() {
    local cfg="$1"
    [[ -r "$cfg" ]] || return 1
    if command -v yq >/dev/null 2>&1; then
        yq -r '.secrets.codex_auth_json.secret_name // ""' "$cfg" 2>/dev/null
    elif command -v python3 >/dev/null 2>&1; then
        python3 - "$cfg" <<'PY' 2>/dev/null
import sys, yaml
try:
    with open(sys.argv[1]) as f:
        cfg = yaml.safe_load(f) or {}
    print(((cfg.get("secrets") or {}).get("codex_auth_json") or {}).get("secret_name", ""))
except Exception:
    sys.exit(1)
PY
    else
        return 1
    fi
}

if [[ -z "$SECRET" ]]; then
    for cfg in "./.ra/config.yaml" "$HOME/.config/ra/config.yaml"; do
        SECRET=$(_lookup_secret_name "$cfg" || true)
        SECRET="${SECRET//$'\n'/}"
        [[ -n "$SECRET" ]] && break
    done
fi

if [[ -z "$SECRET" ]]; then
    echo "could not resolve secret name from .ra/config.yaml or ~/.config/ra/config.yaml" >&2
    echo "pass --secret <name>, or install yq / python3+pyyaml for autodetect" >&2
    exit 1
fi

if [[ -z "$PROJECT" ]]; then
    PROJECT=$(gcloud config get-value project 2>/dev/null || true)
fi
if [[ -z "$PROJECT" ]]; then
    echo "could not resolve GCP project; pass --project <id> or run 'gcloud config set project <id>'" >&2
    exit 1
fi

echo "Adding new version to secret: $SECRET (project: $PROJECT)"
echo "Source: $AUTH_JSON"
base64 < "$AUTH_JSON" | tr -d '\n' \
    | gcloud secrets versions add "$SECRET" --data-file=- --project="$PROJECT"

cat <<EOF

Done. Restart your workstation to pick up the new token, e.g.:
  gcloud workstations stop  <name> --cluster <c> --config <cfg> --region <r>
  gcloud workstations start <name> --cluster <c> --config <cfg> --region <r>
EOF
