#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
APP_NAME="USB Vault"
OUT_DIR="$ROOT_DIR/dist/macos"
VERSION="${1:-}"

if [[ -z "${VERSION}" ]]; then
  latest_dmg="$(ls -1t "$OUT_DIR"/USBVault-*.dmg 2>/dev/null | head -n 1 || true)"
  if [[ -z "$latest_dmg" ]]; then
    echo "Usage: $0 <version>"
    echo "No DMG found under $OUT_DIR to infer version."
    exit 1
  fi
  VERSION="$(basename "$latest_dmg" | sed -E 's/^USBVault-(.*)\.dmg$/\1/')"
fi

APP_DIR="$OUT_DIR/${APP_NAME}.app"
DMG_PATH="$OUT_DIR/USBVault-${VERSION}.dmg"
IDENTITY="${APPLE_DEVELOPER_IDENTITY:-}"
NOTARY_PROFILE="${APPLE_NOTARY_PROFILE:-}"

if [[ -z "$IDENTITY" ]]; then
  echo "Missing APPLE_DEVELOPER_IDENTITY env var"
  echo "Example: Developer ID Application: Your Name (TEAMID)"
  exit 1
fi

if [[ -z "$NOTARY_PROFILE" ]]; then
  echo "Missing APPLE_NOTARY_PROFILE env var"
  echo "Create one with: xcrun notarytool store-credentials <name> ..."
  exit 1
fi

if [[ ! -d "$APP_DIR" ]]; then
  echo "Missing app bundle: $APP_DIR"
  echo "Run scripts/macos/build_app.sh $VERSION first."
  exit 1
fi

if [[ ! -f "$DMG_PATH" ]]; then
  echo "Missing DMG: $DMG_PATH"
  echo "Run scripts/macos/build_dmg.sh $VERSION first."
  exit 1
fi

echo "Signing app binaries..."
codesign --force --timestamp --options runtime --sign "$IDENTITY" "$APP_DIR/Contents/MacOS/usbvaultd"
codesign --force --timestamp --options runtime --sign "$IDENTITY" "$APP_DIR/Contents/MacOS/usbvault-launcher"


echo "Signing app bundle..."
codesign --force --timestamp --options runtime --sign "$IDENTITY" "$APP_DIR"


echo "Verifying app signature..."
codesign --verify --deep --strict --verbose=2 "$APP_DIR"


echo "Signing DMG..."
codesign --force --timestamp --sign "$IDENTITY" "$DMG_PATH"
codesign --verify --verbose=2 "$DMG_PATH"


echo "Submitting DMG for notarization (this can take several minutes)..."
xcrun notarytool submit "$DMG_PATH" --keychain-profile "$NOTARY_PROFILE" --wait


echo "Stapling notarization ticket..."
xcrun stapler staple "$APP_DIR"
xcrun stapler staple "$DMG_PATH"


echo "Validating stapled ticket..."
xcrun stapler validate "$DMG_PATH"


echo "Done. Signed + notarized artifacts:"
echo "  $APP_DIR"
echo "  $DMG_PATH"
