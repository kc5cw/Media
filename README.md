# USB Vault (Go MVP)

USB Vault is a cross-platform local app that:

- Monitors new mounted USB/removable volumes.
- Scans for image/video files (including drone-oriented formats such as `.ts`, `.mpeg`, `.mp4`, `.jpg`, HDR/RAW variants, and others).
- Ingests media into a configured base storage folder.
- Extracts EXIF/GPS/camera metadata where available.
- Computes CRC32 + SHA256 and deduplicates before copy.
- Stores metadata and audit events in SQLite.
- Hosts a local web GUI for setup, auth, gallery, preview player, and map.

## Security model (MVP)

- Local username/password auth (PBKDF2 + random salt).
- Session cookies are HttpOnly + SameSite Strict.
- API responses include hardened security headers.
- Ingested files are copied as read-only (`0440`) and timestamps are preserved.
- Append-only audit log entries include hash chaining for tamper evidence.

## Project layout

- `/cmd/usbvault/main.go` - app entrypoint
- `/internal/app` - HTTP server and API routes
- `/internal/db` - SQLite schema and persistence
- `/internal/usb` - removable media mount watcher
- `/internal/ingest` - scanning and copy/dedupe pipeline
- `/internal/media` - EXIF/hash extraction
- `/internal/security` - password/session primitives
- `/internal/audit` - tamper-evident audit chain
- `/web` - browser UI

## Run

1. Ensure Go 1.26+ is installed.
2. In this folder:

```bash
go mod tidy
go run ./cmd/usbvault
```

3. Open `http://127.0.0.1:4987`.
4. Complete setup with:
   - local username/password
   - absolute base storage path (for example: `/Users/<you>/USBVaultLibrary`)

## Environment variables

- `USBVAULT_PORT` - default `4987`
- `USBVAULT_BIND` - default `127.0.0.1`
- `USBVAULT_DATA_DIR` - default `<cwd>/data`
- `USBVAULT_SCAN_INTERVAL_SECONDS` - default `10`

## Current cloud sync status

Cloud sync is represented as local configuration endpoints (`/api/cloud-sync`) so policy/rules can be saved now and integrated with a provider worker next.

## Important notes

- USB detection is mount-based polling for portability (macOS/Windows/Linux/Raspberry Pi).
- Some advanced DJI telemetry fields depend on how metadata is embedded in the source media; the MVP parses EXIF + common DJI gimbal tags where present.
- Immutable timestamps cannot be guaranteed against admin/root-level OS overrides; integrity verification relies on audit hash chaining and stored checksums.
