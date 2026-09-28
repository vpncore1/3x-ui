#!/usr/bin/env bash
# Public one-command install — 3x-ui v2.9.0 + Outbound Subscriptions + balancer
# Usage:
#   bash <(curl -Ls https://raw.githubusercontent.com/vpncore1/3x-ui/main/install.sh)
# Optional:
#   PANEL_PORT=1872 bash <(curl -Ls https://raw.githubusercontent.com/vpncore1/3x-ui/main/install.sh)
set -euo pipefail

REPO_RAW="${REPO_RAW:-https://raw.githubusercontent.com/vpncore1/3x-ui/main}"
REPO_GIT="${REPO_GIT:-https://github.com/vpncore1/3x-ui.git}"
PATCH_DIR="${PATCH_DIR:-/opt/3x-ui-sub-balancer}"
XUI_TAG="${XUI_TAG:-v2.9.0}"
PANEL_PORT="${PANEL_PORT:-}"
KEEP_CORE="${KEEP_CORE:-1}"

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

[[ $EUID -ne 0 ]] && echo -e "${red}Run as root${plain}" && exit 1

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates git tar rsync build-essential >/dev/null

echo -e "${green}==> Step 1: Install base 3x-ui ${XUI_TAG}${plain}"
if [[ ! -f /usr/local/x-ui/x-ui ]]; then
  curl -Ls https://raw.githubusercontent.com/MHSanaei/3x-ui/master/install.sh -o /tmp/xui-official.sh
  if [[ -n "$PANEL_PORT" ]]; then
    # noninteractive-ish: confirm defaults then set port if prompted path differs by version
    bash /tmp/xui-official.sh "$XUI_TAG" || true
  else
    bash /tmp/xui-official.sh "$XUI_TAG" || true
  fi
fi
if [[ ! -f /usr/local/x-ui/x-ui ]]; then
  echo -e "${red}Base 3x-ui install failed${plain}"
  exit 1
fi

if [[ -n "$PANEL_PORT" ]]; then
  /usr/local/x-ui/x-ui setting -port "$PANEL_PORT" || true
fi

echo -e "${green}==> Step 2: Fetch patch sources (public)${plain}"
rm -rf "$PATCH_DIR"
git clone --depth 1 "$REPO_GIT" "$PATCH_DIR"

echo -e "${green}==> Step 3: Build & install patched panel (KEEP_CORE=${KEEP_CORE})${plain}"
export XUI_TAG KEEP_CORE
export BUILD_DIR="${BUILD_DIR:-/opt/3x-ui-build}"
export INSTALL_BIN="/usr/local/x-ui/x-ui"
bash "$PATCH_DIR/panel-patch/build_on_server.sh"

echo -e "${green}Done.${plain}"
/usr/local/x-ui/x-ui setting -show true 2>/dev/null || true
echo -e "${yellow}Panel: Xray → Outbounds → Subscriptions${plain}"
