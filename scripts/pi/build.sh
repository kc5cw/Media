#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/dist/pi"
VERSION="${1:-$(date +%Y.%m.%d.%H%M)}"

mkdir -p "$OUT_DIR"

# Build for common Raspberry Pi OS targets.
# - armv7: 32-bit Pi OS on Pi 2/3/4
# - arm64: 64-bit Pi OS on Pi 3/4/5

echo "Building usbvaultd + usbvault-kiosk for linux/armv7..."
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$OUT_DIR/usbvaultd_linux_armv7_${VERSION}" "$ROOT_DIR/cmd/usbvault"
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$OUT_DIR/usbvault-kiosk_linux_armv7_${VERSION}" "$ROOT_DIR/cmd/usbvault-kiosk"


echo "Building usbvaultd + usbvault-kiosk for linux/arm64..."
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$OUT_DIR/usbvaultd_linux_arm64_${VERSION}" "$ROOT_DIR/cmd/usbvault"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$OUT_DIR/usbvault-kiosk_linux_arm64_${VERSION}" "$ROOT_DIR/cmd/usbvault-kiosk"


echo "Done: $OUT_DIR"
