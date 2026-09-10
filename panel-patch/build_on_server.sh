#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Always use this script's directory (panel-patch/), ignore PATCH_DIR from parent.
PATCH_DIR="$SCRIPT_DIR"
BUILD_DIR="${BUILD_DIR:-/opt/3x-ui-build}"
TAG="${XUI_TAG:-v3.3.0}"
INSTALL_BIN="${INSTALL_BIN:-/usr/local/x-ui/x-ui}"
LOCK="/tmp/panel-patch-build.lock"

exec 9>"$LOCK"
if ! flock -n 9; then
  echo "Another build is running. Exit."
  exit 1
fi

echo "==> Install build deps"
export DEBIAN_FRONTEND=noninteractive
apt-get install -y -qq git rsync build-essential curl xz-utils >/dev/null 2>&1 || true

if ! command -v node >/dev/null || [[ "$(node -v | cut -d. -f1 | tr -d v)" -lt 20 ]]; then
  NODE_VER="v22.14.0"
  curl -fsSL "https://nodejs.org/dist/${NODE_VER}/node-${NODE_VER}-linux-x64.tar.xz" -o /tmp/node.tar.xz
  tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1
fi

if ! command -v go >/dev/null || [[ "$(go version | awk '{print $3}' | tr -d go | cut -d. -f1,2)" < "1.22" ]]; then
  GO_VER="1.23.8"
  curl -fsSL "https://mirrors.aliyun.com/golang/go${GO_VER}.linux-amd64.tar.gz" -o /tmp/go.tgz \
    || curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tgz
  export PATH="/usr/local/go/bin:$PATH"
fi
export PATH="/usr/local/go/bin:$PATH"

echo "==> Prepare source ($TAG) in $BUILD_DIR"
rm -rf "$BUILD_DIR"
git clone --depth 1 --branch "$TAG" https://github.com/MHSanaei/3x-ui.git "$BUILD_DIR"

echo "==> Apply panel patch"
python3 "$PATCH_DIR/apply_patch.py" "$BUILD_DIR"

echo "==> Build frontend"
cd "$BUILD_DIR/frontend"
pkill -f "vite build" 2>/dev/null || true
rm -rf node_modules
npm ci
npm run gen:api
npx vite build

echo "==> Build x-ui binary"
cd "$BUILD_DIR"
mkdir -p build
export CGO_ENABLED=1
go mod download
go build -ldflags "-w -s" -o build/x-ui main.go

echo "==> Install"
systemctl stop x-ui || true
install -m 755 "$BUILD_DIR/build/x-ui" "$INSTALL_BIN"
mkdir -p /usr/local/x-ui/web/dist /usr/local/x-ui/web/translation
rsync -a --delete "$BUILD_DIR/web/dist/" /usr/local/x-ui/web/dist/
rsync -a "$BUILD_DIR/web/translation/" /usr/local/x-ui/web/translation/
systemctl start x-ui
sleep 2
systemctl is-active x-ui
echo "Done."
