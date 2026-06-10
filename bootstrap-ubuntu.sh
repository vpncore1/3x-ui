#!/usr/bin/env bash
# Paste and run as root on Ubuntu — one block, verbose output.
set -ex

: "${GITHUB_TOKEN:?Set GITHUB_TOKEN first: export GITHUB_TOKEN=gho_xxxx}"
PATCH_DIR="/opt/3x-ui-sub-balancer"
TOKEN_FILE="/root/.3x-ui-sub-balancer-token"

echo "=== [1/4] root check ==="
id
uname -a

echo "=== [2/4] install git curl ==="
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y git curl ca-certificates rsync build-essential

echo "=== [3/4] clone private repo ==="
rm -rf "$PATCH_DIR"
git clone --depth 1 --branch main \
  "https://x-access-token:${GITHUB_TOKEN}@github.com/sader21/3x-ui-sub-balancer.git" \
  "$PATCH_DIR"
git -C "$PATCH_DIR" remote set-url origin "https://github.com/sader21/3x-ui-sub-balancer.git"
ls -la "$PATCH_DIR"

printf '%s' "$GITHUB_TOKEN" > "$TOKEN_FILE"
chmod 600 "$TOKEN_FILE"

echo "=== [4/4] run install ==="
export GITHUB_TOKEN
bash "$PATCH_DIR/ubuntu-install.sh"

echo "=== DONE ==="
