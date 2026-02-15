#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
APP_NAME="USB Vault"
OUT_DIR="$ROOT_DIR/dist/macos"
APP_DIR="$OUT_DIR/${APP_NAME}.app"
VERSION="${1:-$(date +%Y.%m.%d.%H%M)}"
DMG_PATH="$OUT_DIR/USBVault-${VERSION}.dmg"

if [[ ! -d "$APP_DIR" ]]; then
  echo "App bundle not found at: $APP_DIR"
  echo "Run scripts/macos/build_app.sh first."
  exit 1
fi

STAGE_DIR="$(mktemp -d)"
trap 'rm -rf "$STAGE_DIR"' EXIT

ditto "$APP_DIR" "$STAGE_DIR/${APP_NAME}.app"
ln -s /Applications "$STAGE_DIR/Applications"

hdiutil create \
  -volname "USB Vault Installer" \
  -srcfolder "$STAGE_DIR" \
  -ov \
  -format UDZO \
  "$DMG_PATH" >/dev/null

echo "Built DMG: $DMG_PATH"
