#!/bin/sh
# Cross-builds "TLOU Mod Installer.exe" from Linux/macOS. Needs Go 1.24 or newer.
set -e
cd "$(dirname "$0")"
go mod download
[ -f icon.ico ] || base64 -d icon.ico.b64 > icon.ico
go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -ico icon.ico -arch amd64 -o rsrc_windows_amd64.syso
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o "TLOU Mod Installer.exe" .
sha256sum "TLOU Mod Installer.exe"
