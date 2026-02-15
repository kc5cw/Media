# USB Vault

USB Vault is a local-first media ingest app for drone and camera workflows.

It detects removable drives, scans for image/video media, extracts metadata (EXIF/GPS when available), deduplicates with CRC32 + SHA256, and stores files in a local library with a browser-based gallery, map view, preview player, and audit trail.

## Key Features

- Automatic removable drive discovery (macOS/Windows/Linux/Raspberry Pi mount paths).
- Media scanning for common video/image formats, including drone-oriented formats (`.ts`, `.mpeg`, `.mp4`, `.jpg`, HDR/RAW variants, and more).
- Metadata extraction (capture time, GPS, camera make/model, DJI gimbal values when embedded).
- Duplicate prevention using checksum and capture-time matching.
- Secure local auth (username/password) and session cookies.
- Tamper-evident audit log chain for key system events.
- Local web GUI for album browsing, sorting, map markers, and preview playback.

## Install (macOS Users)

1. Download the latest DMG from [Releases](https://github.com/kc5cw/Media/releases).
2. Open the DMG.
3. Drag `USB Vault.app` into `Applications`.
4. Launch `USB Vault.app` from Finder or Spotlight.
5. Your browser opens automatically to `http://127.0.0.1:4987`.

## Raspberry Pi (HDMI + Touchscreen Kiosk)\n+\n+USB Vault can run headless on a Pi, or it can launch a local kiosk UI if a display is connected.\n+\n+Behavior on Pi (Linux):\n+\n+- If HDMI is connected: show the standard UI (same as macOS).\n+- If a touchscreen is detected and HDMI is not connected: show a touch-optimized UI.\n+\n+Install instructions (manual):\n+\n+- See `scripts/pi/install.md`.\n+\n+Important note:\n+\n+- GPIO pins do not carry video by themselves; most “GPIO tiny screens” are SPI/DSI displays that still appear to Linux as a framebuffer device.\n+\n ## First-Time Setup
## First-Time Setup

1. Create a local username/password.
2. Choose a base storage directory (absolute path).
3. Insert a USB drive with media files.
4. USB Vault imports supported media automatically and updates the album/map.

Data and logs on macOS:

- App data: `~/Library/Application Support/USBVault/data`
- Logs: `~/Library/Logs/USBVault/usbvault.log`

## Running From Source

Requirements:

- Go 1.26+

Commands:

```bash
go mod tidy
go run ./cmd/usbvault
```

Then open `http://127.0.0.1:4987`.

## Build a macOS App + DMG (Unsigned)

```bash
./scripts/macos/build_app.sh 0.2.0
./scripts/macos/build_dmg.sh 0.2.0
```

Outputs:

- `dist/macos/USB Vault.app`
- `dist/macos/USBVault-0.2.0.dmg`

## Build Signed + Notarized macOS Release

Prerequisites:

- Apple Developer account
- `Developer ID Application` certificate installed in Keychain
- Xcode command line tools (`xcode-select --install`)
- Notary profile configured once with `xcrun notarytool`

One-time notary profile setup example:

```bash
xcrun notarytool store-credentials usbvault-notary \
  --apple-id "YOUR_APPLE_ID" \
  --team-id "YOUR_TEAM_ID" \
  --password "APP_SPECIFIC_PASSWORD"
```

Set environment variables:

```bash
export APPLE_DEVELOPER_IDENTITY="Developer ID Application: Your Name (TEAMID)"
export APPLE_NOTARY_PROFILE="usbvault-notary"
```

Create signed/notarized release artifacts:

```bash
./scripts/macos/release_signed.sh 0.2.0
```

This script will:

1. Build universal app binaries (`arm64` + `amd64`).
2. Build app bundle.
3. Build DMG installer.
4. Sign app + DMG.
5. Submit DMG to notarization and wait.
6. Staple notarization tickets.

## Publish to GitHub Releases

After `release_signed.sh` finishes, upload the generated DMG from `dist/macos/` to a new GitHub release tag.

## Environment Variables

- `USBVAULT_PORT` (default: `4987`)
- `USBVAULT_BIND` (default: `127.0.0.1`)
- `USBVAULT_DATA_DIR` (default platform config path; macOS app uses `~/Library/Application Support/USBVault/data`)
- `USBVAULT_WEB_DIR` (optional web asset override)
- `USBVAULT_SCAN_INTERVAL_SECONDS` (default: `10`)

## Security Notes

- Passwords are stored as PBKDF2 hashes with random salts.
- Session cookie is `HttpOnly` + `SameSite=Strict`.
- Imported files are copied read-only.
- Audit entries are hash-chained for tamper evidence.
- Administrator/root users can still override filesystem timestamps; integrity checks should rely on stored checksums and audit history.

## Project Layout

- `cmd/usbvault` - backend server entrypoint
- `cmd/usbvault-launcher` - macOS GUI launcher entrypoint
- `internal/app` - HTTP server and API routes
- `internal/usb` - mount polling watcher
- `internal/ingest` - scanning/copy/dedupe pipeline
- `internal/media` - hash and metadata extraction
- `internal/db` - SQLite schema/storage
- `internal/security` - password/session primitives
- `internal/audit` - audit hash chain
- `web` - hosted GUI assets
- `scripts/macos` - macOS packaging/sign/notarize scripts
