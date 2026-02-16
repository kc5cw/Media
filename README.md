# USB Vault

USB Vault is a local-first media ingest app for drone and camera workflows.

It detects removable drives, scans for image/video media, extracts metadata (EXIF/GPS when available), deduplicates with CRC32 + SHA256, and stores files in a local library with a browser-based gallery, map view, preview player, and audit trail.

## Key Features

- Automatic removable drive discovery (macOS/Windows/Linux/Raspberry Pi mount paths).
- Media scanning for common video/image formats, including drone-oriented formats (`.ts`, `.mpeg`, `.mp4`, `.jpg`, HDR/RAW variants, and more).
- Metadata extraction (capture time, GPS, camera make/model, DJI gimbal values when embedded).
- Duplicate prevention using checksum and capture-time matching.
- Local auth (username/password) and session cookies.
- Tamper-evident audit log chain for key system events.
- Local web GUI for album browsing, sorting, map markers, and preview playback.
- Advanced media filters (location + type + GPS + date range + text search).
- Download options for single/multi-select: parallel file downloads or ZIP bundle.
- User Albums with add/remove selected media while preserving the All Media library.
- Auto album folders from EXIF/geocoded location metadata in Album view.
- Extended sorting for EXIF/DJI fields (gimbal yaw/pitch/roll, camera make/model, GPS, proximity).
- Multi-select delete workflow with confirmation and DB/file cleanup.
- Streaming backup export to `ssh`, `rsync`, `s3`, or generic `api` endpoints.
- Storage layout that matches the UI location tree (state/county/city/street/date).

## Quick Start (Source)

Requirements:

- Go 1.26+

From repo root:

```bash
go mod tidy
go run ./cmd/usbvault
```

Open [http://127.0.0.1:4987](http://127.0.0.1:4987).

## Run as a Background Service

Use the platform service manager so USB Vault does not tie up a terminal.

- Full guide: `docs/run-as-service.md`

### macOS (launchd)

```bash
./scripts/macos/install_launchd.sh
```

Useful commands:

```bash
./scripts/macos/status_launchd.sh
./scripts/macos/uninstall_launchd.sh --keep-plist
./scripts/macos/uninstall_launchd.sh
```

### Linux / Raspberry Pi (systemd)

Use the included unit files under `scripts/pi/systemd/`.

### Windows 10/11

Use Task Scheduler or NSSM (documented in `docs/run-as-service.md`).

## macOS App + DMG

Installable app users can double-click:

1. Download latest DMG from [Releases](https://github.com/kc5cw/Media/releases).
2. Open DMG.
3. Drag `USB Vault.app` to Applications.
4. Launch `USB Vault.app`.

Build unsigned local artifacts:

```bash
./scripts/macos/build_app.sh 0.2.0
./scripts/macos/build_dmg.sh 0.2.0
```

Outputs:

- `dist/macos/USB Vault.app`
- `dist/macos/USBVault-0.2.0.dmg`

## Raspberry Pi (HDMI + Touchscreen Kiosk)

USB Vault can run headless on Pi, or launch a kiosk UI if display input devices are present.

- Install/build guide: `scripts/pi/install.md`
- If HDMI is connected: standard UI.
- If touch display is detected (without HDMI): touch-optimized UI.
- Note: GPIO pins do not carry video directly; SPI/DSI displays appear as Linux display/framebuffer devices.

## First-Time Setup

1. Create a local username/password.
2. Choose a base storage directory (absolute path).
3. Insert a USB drive with media files.
4. USB Vault imports supported media automatically and updates the album/map.

Data and logs on macOS (source/default):

- App data: `~/Library/Application Support/USBVault/data`
- Logs: `~/Library/Logs/USBVault/`

## Storage Layout

Default layout:

`<base>/State/County/City/Street/YYYY/MM/DD/<filename>_<hash>.<ext>`

Fallback without geocoded location:

`<base>/Unknown/YYYY/MM/DD/...`

Supported modes:

- `location_date` (default)
- `date`

## Delete Media (GUI)

From **Media Library**:

- `Select All Shown` selects current filtered set.
- Click item checkboxes to deselect specific files.
- `Delete Selected` (or `Delete Current` in Preview Player).
- Confirm deletion; USB Vault removes files from disk and DB rows.

## Filter + Download (GUI)

From **Media Library**:

- Filter by location, type (`image`/`video`), GPS presence, capture date range, and text search.
- Use `Download Files` for per-file browser downloads (parallel TCP sessions, browser-limited).
- Use `Download ZIP` to export selected files in one archive stream.
- In **Preview Player**, use `Download Current` for a single item.

## Albums + Advanced Sorting (GUI)

- `All Media` keeps the full library view.
- `Albums` view lets you:
  - create albums,
  - select an album,
  - add/remove selected media.
- `Auto Folders (EXIF Location)` appear in album mode for location-based grouping.
- Sort options include:
  - capture/ingested time,
  - file metadata (name, size, kind, extension),
  - camera fields (make/model, gimbal yaw/pitch/roll),
  - location fields (state/county/city/street, lat/lon),
  - `region proximity` using `near_lat` + `near_lon`.

## Backup Export (GUI)

Use **Backup Export** to avoid creating a second full local archive:

- `SSH`: streams `tar.gz` directly to `user@host:/path/file.tar.gz`
- `S3`: streams `tar.gz` using `aws s3 cp - s3://bucket/key.tar.gz`
- `API`: streams `tar.gz` via `PUT` or `POST`
- `Rsync`: compressed transfer sync (`rsync -az`)

## Environment Variables

- `USBVAULT_PORT` (default `4987`)
- `USBVAULT_BIND` (default `127.0.0.1`)
- `USBVAULT_DATA_DIR` (default platform config path)
- `USBVAULT_WEB_DIR` (optional web asset override)
- `USBVAULT_SCAN_INTERVAL_SECONDS` (default `10`)

## Security Notes

- Passwords are stored as PBKDF2 hashes with random salts.
- Session cookies use `HttpOnly` and `SameSite=Strict`.
- Imported files are copied read-only.
- Audit entries are hash-chained for tamper evidence.
- Administrator/root users can still alter filesystem timestamps; rely on checksums + audit records for integrity.

## Project Layout

- `cmd/usbvault` - backend server entrypoint
- `cmd/usbvault-launcher` - macOS launcher entrypoint
- `cmd/usbvault-kiosk` - kiosk UI launcher for Pi/Linux
- `internal/app` - HTTP server and API routes
- `internal/usb` - mount polling watcher
- `internal/ingest` - scanning/copy/dedupe pipeline
- `internal/media` - hash + metadata extraction
- `internal/db` - SQLite schema/storage
- `internal/security` - password/session primitives
- `internal/audit` - audit hash chain
- `web` - hosted GUI assets
- `scripts/macos` - app packaging and launchd helpers
- `scripts/pi` - Pi build/install/systemd helpers
