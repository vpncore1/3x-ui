#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PATCH_DIR="$SCRIPT_DIR"
BUILD_DIR="${BUILD_DIR:-/opt/3x-ui-build}"
TAG="${XUI_TAG:-v2.9.0}"
INSTALL_BIN="${INSTALL_BIN:-/usr/local/x-ui/x-ui}"
LOCK="/tmp/panel-patch-build.lock"
KEEP_CORE="${KEEP_CORE:-1}"

exec 9>"$LOCK"
if ! flock -n 9; then
  echo "Another build is running. Exit."
  exit 1
fi

echo "==> Install build deps"
export DEBIAN_FRONTEND=noninteractive
apt-get install -y -qq git build-essential curl xz-utils rsync >/dev/null 2>&1 || true

if ! command -v go >/dev/null || [[ "$(go version | awk '{print $3}' | tr -d go | cut -d. -f1,2)" < "1.22" ]]; then
  GO_VER="1.26.4"
  curl -fsSL "https://mirrors.aliyun.com/golang/go${GO_VER}.linux-amd64.tar.gz" -o /tmp/go.tgz \
    || curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tgz
fi
export PATH="/usr/local/go/bin:$PATH"

echo "==> Prepare source ($TAG) in $BUILD_DIR"
rm -rf "$BUILD_DIR"
git clone --depth 1 --branch "$TAG" https://github.com/MHSanaei/3x-ui.git "$BUILD_DIR"

echo "==> Apply panel patch (subscriptions + balancer)"
python3 "$PATCH_DIR/apply_patch.py" "$BUILD_DIR"

echo "==> Build x-ui binary (Vue assets are already in web/ — no npm)"
cd "$BUILD_DIR"
mkdir -p build
export CGO_ENABLED=1
go mod tidy
go build -ldflags "-w -s -X 'github.com/mhsanaei/3x-ui/v2/config.version=${TAG}-sub-balancer'" -o build/x-ui main.go

echo "==> Install binary (KEEP_CORE=$KEEP_CORE — xray core not touched)"
systemctl stop x-ui || true
install -m 755 "$BUILD_DIR/build/x-ui" "$INSTALL_BIN"
# Refresh HTML/assets/translations from patched tree (Vue UI)
mkdir -p /usr/local/x-ui/web
rsync -a --delete "$BUILD_DIR/web/html/" /usr/local/x-ui/web/html/ 2>/dev/null || true
rsync -a "$BUILD_DIR/web/translation/" /usr/local/x-ui/web/translation/ 2>/dev/null || true
rsync -a "$BUILD_DIR/web/assets/" /usr/local/x-ui/web/assets/ 2>/dev/null || true

# Stamp installed patch commit so the in-panel Update button can detect new versions.
# Source is the cloned vpncore1/3x-ui tree when run via upgrade.sh (parent of panel-patch).
PATCH_REPO_DIR="$(cd "$PATCH_DIR/.." && pwd)"
PATCH_SHA="$(git -C "$PATCH_REPO_DIR" rev-parse HEAD 2>/dev/null || true)"
if [[ -z "$PATCH_SHA" ]]; then
  PATCH_SHA="$(git -C "$PATCH_DIR" rev-parse HEAD 2>/dev/null || true)"
fi
if [[ -n "$PATCH_SHA" ]]; then
  cat > /usr/local/x-ui/panel-patch.version <<EOF
sha=${PATCH_SHA}
version=${TAG}-sub-balancer
updated_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
repo=vpncore1/3x-ui
branch=main
EOF
  echo "==> Wrote panel-patch.version sha=${PATCH_SHA:0:7}"
fi

systemctl start x-ui
sleep 2
systemctl is-active x-ui
echo "Done. Base=$TAG with Outbound Subscriptions + balancer runtime sync."
