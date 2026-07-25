#!/usr/bin/env bash
# Expand ${VAR} in dex-config.yaml.template using current shell env / .env
set -euo pipefail
cd "$(dirname "$0")"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

: "${DEX_CLIENT_SECRET:?DEX_CLIENT_SECRET required}"
: "${GITHUB_CLIENT_ID:?GITHUB_CLIENT_ID required}"
: "${GITHUB_CLIENT_SECRET:?GITHUB_CLIENT_SECRET required}"

if command -v envsubst >/dev/null 2>&1; then
  envsubst < dex-config.yaml.template > dex-config.yaml
else
  # minimal fallback without gettext
  sed \
    -e "s|\${DEX_CLIENT_SECRET}|${DEX_CLIENT_SECRET}|g" \
    -e "s|\${GITHUB_CLIENT_ID}|${GITHUB_CLIENT_ID}|g" \
    -e "s|\${GITHUB_CLIENT_SECRET}|${GITHUB_CLIENT_SECRET}|g" \
    dex-config.yaml.template > dex-config.yaml
fi

echo "wrote $(pwd)/dex-config.yaml"
