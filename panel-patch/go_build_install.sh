#!/bin/bash
set -euo pipefail
exec > /tmp/panel-go-build.log 2>&1
cd /opt/3x-ui-build
export CGO_ENABLED=1
go build -ldflags "-w -s" -o build/x-ui main.go
systemctl stop x-ui || true
install -m 755 build/x-ui /usr/local/x-ui/x-ui
mkdir -p /usr/local/x-ui/web/dist /usr/local/x-ui/web/translation
rsync -a --delete web/dist/ /usr/local/x-ui/web/dist/
rsync -a web/translation/ /usr/local/x-ui/web/translation/
systemctl start x-ui
sleep 2
systemctl is-active x-ui
/usr/local/x-ui/x-ui -v | head -1
echo DONE
