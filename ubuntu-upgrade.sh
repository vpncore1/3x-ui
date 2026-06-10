#!/usr/bin/env bash
# Upgrade existing install on Ubuntu/Debian.
set -euo pipefail

TOKEN_FILE="/root/.3x-ui-sub-balancer-token"
PATCH_DIR="/opt/3x-ui-sub-balancer"

[[ $EUID -ne 0 ]] && echo "Run as root" && exit 1

if [[ -z "${GITHUB_TOKEN:-}" ]] && [[ -f "$TOKEN_FILE" ]]; then
  export GITHUB_TOKEN="$(tr -d '\r\n' < "$TOKEN_FILE")"
fi

export FORCE_GIT_PULL=1
bash "$PATCH_DIR/upgrade.sh"
