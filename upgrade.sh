#!/usr/bin/env bash
# Upgrade an existing 3x-ui install with the subscription + balancer patch.
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/sader21/3x-ui-sub-balancer.git}"
REPO_BRANCH="${REPO_BRANCH:-main}"
PATCH_DIR="${PATCH_DIR:-/opt/3x-ui-sub-balancer}"
XUI_TAG="${XUI_TAG:-v3.3.0}"

red='\033[0;31m'
green='\033[0;32m'
plain='\033[0m'

[[ $EUID -ne 0 ]] && echo -e "${red}Run as root${plain}" && exit 1

if [[ ! -f /usr/local/x-ui/x-ui ]]; then
  echo -e "${red}3x-ui is not installed. Run install.sh first.${plain}"
  exit 1
fi

echo -e "${green}==> Fetch patch repo${plain}"
apt-get install -y -qq git curl rsync build-essential >/dev/null 2>&1 || true

if [[ -d "$PATCH_DIR/.git" ]]; then
  git -C "$PATCH_DIR" fetch origin "$REPO_BRANCH"
  git -C "$PATCH_DIR" reset --hard "origin/$REPO_BRANCH"
else
  rm -rf "$PATCH_DIR"
  git clone --depth 1 --branch "$REPO_BRANCH" "$REPO_URL" "$PATCH_DIR"
fi

export PATCH_DIR
export XUI_TAG
export BUILD_DIR="${BUILD_DIR:-/opt/3x-ui-build}"
export INSTALL_BIN="/usr/local/x-ui/x-ui"

bash "$PATCH_DIR/panel-patch/build_on_server.sh"

echo -e "${green}Upgrade complete.${plain}"
