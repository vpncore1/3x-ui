#!/usr/bin/env bash
# Upgrade an existing 3x-ui install with the subscription + balancer patch.
set -euo pipefail

GITHUB_USER="${GITHUB_USER:-sader21}"
REPO_NAME="${REPO_NAME:-3x-ui-sub-balancer}"
REPO_BRANCH="${REPO_BRANCH:-main}"
PATCH_DIR="${PATCH_DIR:-/opt/3x-ui-sub-balancer}"
XUI_TAG="${XUI_TAG:-v3.3.0}"

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

[[ $EUID -ne 0 ]] && echo -e "${red}Run as root${plain}" && exit 1

if [[ ! -f /usr/local/x-ui/x-ui ]]; then
  echo -e "${red}3x-ui is not installed. Run install.sh first.${plain}"
  exit 1
fi

repo_clone_url() {
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    echo "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_USER}/${REPO_NAME}.git"
  elif [[ -n "${REPO_URL:-}" ]]; then
    echo "$REPO_URL"
  else
    echo "https://github.com/${GITHUB_USER}/${REPO_NAME}.git"
  fi
}

echo -e "${green}==> Fetch patch repo${plain}"
apt-get install -y -qq git curl rsync build-essential >/dev/null 2>&1 || true

if [[ -f "$PATCH_DIR/panel-patch/build_on_server.sh" ]] && [[ "${FORCE_GIT_PULL:-0}" != "1" ]]; then
  echo "Using existing patch at $PATCH_DIR"
elif [[ -d "$PATCH_DIR/.git" ]]; then
  if [[ -z "${GITHUB_TOKEN:-}" ]]; then
    echo -e "${yellow}Private repo: set GITHUB_TOKEN to pull updates, using local copy.${plain}"
  else
    git -C "$PATCH_DIR" remote set-url origin "$(repo_clone_url)"
    git -C "$PATCH_DIR" fetch origin "$REPO_BRANCH"
    git -C "$PATCH_DIR" reset --hard "origin/$REPO_BRANCH"
  fi
else
  CLONE_URL="$(repo_clone_url)"
  if [[ "$CLONE_URL" == https://github.com/* ]] && [[ -z "${GITHUB_TOKEN:-}" ]]; then
    echo -e "${red}Private repo needs GITHUB_TOKEN (PAT with repo scope).${plain}"
    echo -e "${yellow}Example:${plain}"
    echo "  export GITHUB_TOKEN=ghp_xxxxxxxx"
    echo "  bash upgrade.sh"
    exit 1
  fi
  rm -rf "$PATCH_DIR"
  git clone --depth 1 --branch "$REPO_BRANCH" "$CLONE_URL" "$PATCH_DIR"
  # Do not keep token in git remote config
  git -C "$PATCH_DIR" remote set-url origin "https://github.com/${GITHUB_USER}/${REPO_NAME}.git"
fi

export XUI_TAG
export BUILD_DIR="${BUILD_DIR:-/opt/3x-ui-build}"
export INSTALL_BIN="/usr/local/x-ui/x-ui"

bash "$PATCH_DIR/panel-patch/build_on_server.sh"

echo -e "${green}Upgrade complete.${plain}"
