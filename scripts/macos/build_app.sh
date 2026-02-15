#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
APP_NAME="USB Vault"
BUNDLE_ID="com.kc5cw.usbvault"
VERSION="${1:-$(date +%Y.%m.%d.%H%M)}"
OUT_DIR="$ROOT_DIR/dist/macos"
BUILD_DIR="$OUT_DIR/build"
APP_DIR="$OUT_DIR/${APP_NAME}.app"

mkdir -p "$OUT_DIR" "$BUILD_DIR"
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources/web"

echo "Building USB Vault binaries (darwin arm64 + amd64)..."
for arch in arm64 amd64; do
  GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$BUILD_DIR/usbvaultd_${arch}" "$ROOT_DIR/cmd/usbvault"
  GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$BUILD_DIR/usbvault-launcher_${arch}" "$ROOT_DIR/cmd/usbvault-launcher"
done

lipo -create "$BUILD_DIR/usbvaultd_arm64" "$BUILD_DIR/usbvaultd_amd64" -output "$APP_DIR/Contents/MacOS/usbvaultd"
lipo -create "$BUILD_DIR/usbvault-launcher_arm64" "$BUILD_DIR/usbvault-launcher_amd64" -output "$APP_DIR/Contents/MacOS/usbvault-launcher"
chmod 755 "$APP_DIR/Contents/MacOS/usbvaultd" "$APP_DIR/Contents/MacOS/usbvault-launcher"

rsync -a --delete "$ROOT_DIR/web/" "$APP_DIR/Contents/Resources/web/"

cat > "$APP_DIR/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDisplayName</key>
  <string>${APP_NAME}</string>
  <key>CFBundleName</key>
  <string>${APP_NAME}</string>
  <key>CFBundleIdentifier</key>
  <string>${BUNDLE_ID}</string>
  <key>CFBundleExecutable</key>
  <string>usbvault-launcher</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleVersion</key>
  <string>${VERSION}</string>
  <key>CFBundleShortVersionString</key>
  <string>${VERSION}</string>
  <key>LSMinimumSystemVersion</key>
  <string>13.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST

echo "Built app bundle: $APP_DIR"
echo "Launch by double-clicking in Finder. Logs: ~/Library/Logs/USBVault/usbvault.log"
