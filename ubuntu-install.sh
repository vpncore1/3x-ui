#!/usr/bin/env bash
# One-shot install on Ubuntu/Debian (private repo).
# Usage:
#   export GITHUB_TOKEN=gho_xxxx   # or: echo 'gho_xxxx' > /root/.3x-ui-sub-balancer-token && chmod 600 ...
#   bash ubuntu-install.sh
set -euo pipefail

TOKEN_FILE="/root/.3x-ui-sub-balancer-token"
PATCH_DIR="/opt/3x-ui-sub-balancer"
GITHUB_USER="${GITHUB_USER:-sader21}"
REPO_NAME="${REPO_NAME:-3x-ui-sub-balancer}"

red='\033[0;31m'
green='\033[0;32m'
plain='\033[0m'

[[ $EUID -ne 0 ]] && echo -e "${red}Run as root: sudo bash ubuntu-install.sh${plain}" && exit 1

if [[ -z "${GITHUB_TOKEN:-}" ]] && [[ -f "$TOKEN_FILE" ]]; then
  GITHUB_TOKEN="$(tr -d '\r\n' < "$TOKEN_FILE")"
  export GITHUB_TOKEN
fi

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo -e "${red}GITHUB_TOKEN is required for private repo.${plain}"
  echo "  export GITHUB_TOKEN=gho_xxxxxxxx"
  echo "  bash ubuntu-install.sh"
  exit 1
fi

apt-get update -qq
apt-get install -y -qq git curl ca-certificates

if [[ ! -d "$PATCH_DIR/.git" ]]; then
  git clone --depth 1 --branch main \
    "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_USER}/${REPO_NAME}.git" \
    "$PATCH_DIR"
  git -C "$PATCH_DIR" remote set-url origin "https://github.com/${GITHUB_USER}/${REPO_NAME}.git"
else
  git -C "$PATCH_DIR" remote set-url origin "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_USER}/${REPO_NAME}.git"
  git -C "$PATCH_DIR" fetch origin main
  git -C "$PATCH_DIR" reset --hard origin/main
  git -C "$PATCH_DIR" remote set-url origin "https://github.com/${GITHUB_USER}/${REPO_NAME}.git"
fi

# Save token for future upgrades (optional)
if [[ ! -f "$TOKEN_FILE" ]]; then
  printf '%s' "$GITHUB_TOKEN" > "$TOKEN_FILE"
  chmod 600 "$TOKEN_FILE"
fi

bash "$PATCH_DIR/install.sh"

echo -e "${green}Installed. Future upgrades:${plain}"
echo "  bash $PATCH_DIR/upgrade.sh"
