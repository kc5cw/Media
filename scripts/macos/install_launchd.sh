#!/usr/bin/env bash
set -euo pipefail

LABEL="com.kc5cw.usbvault"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

PORT="${1:-4987}"

LAUNCH_AGENTS_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$LAUNCH_AGENTS_DIR/$LABEL.plist"

SUPPORT_DIR="$HOME/Library/Application Support/USBVault"
BIN_DIR="$SUPPORT_DIR/bin"
WEB_DIR="$SUPPORT_DIR/web"
DATA_DIR="$SUPPORT_DIR/data"
LOG_DIR="$HOME/Library/Logs/USBVault"

SERVER_BIN="$BIN_DIR/usbvaultd"
STDOUT_LOG="$LOG_DIR/usbvault-launchd.out.log"
STDERR_LOG="$LOG_DIR/usbvault-launchd.err.log"

echo "Preparing directories..."
mkdir -p "$LAUNCH_AGENTS_DIR" "$BIN_DIR" "$WEB_DIR" "$DATA_DIR" "$LOG_DIR"

echo "Building usbvaultd..."
cd "$ROOT_DIR"
go build -trimpath -ldflags='-s -w' -o "$SERVER_BIN" ./cmd/usbvault

echo "Syncing web assets..."
rsync -a --delete "$ROOT_DIR/web/" "$WEB_DIR/"

echo "Writing LaunchAgent plist: $PLIST_PATH"
cat >"$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$LABEL</string>

  <key>ProgramArguments</key>
  <array>
    <string>$SERVER_BIN</string>
  </array>

  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>

  <key>EnvironmentVariables</key>
  <dict>
    <key>USBVAULT_BIND</key>
    <string>127.0.0.1</string>
    <key>USBVAULT_PORT</key>
    <string>$PORT</string>
    <key>USBVAULT_DATA_DIR</key>
    <string>$DATA_DIR</string>
    <key>USBVAULT_WEB_DIR</key>
    <string>$WEB_DIR</string>
  </dict>

  <key>StandardOutPath</key>
  <string>$STDOUT_LOG</string>
  <key>StandardErrorPath</key>
  <string>$STDERR_LOG</string>
</dict>
</plist>
PLIST

if command -v plutil >/dev/null 2>&1; then
  plutil -lint "$PLIST_PATH"
fi

if launchctl print "gui/$UID/$LABEL" >/dev/null 2>&1; then
  echo "Stopping existing LaunchAgent..."
  launchctl bootout "gui/$UID/$LABEL" || true
fi

if command -v lsof >/dev/null 2>&1; then
  LISTENER_PID="$(lsof -nP -iTCP:$PORT -sTCP:LISTEN -t 2>/dev/null | head -n 1 || true)"
  if [[ -n "$LISTENER_PID" ]]; then
    echo "Port $PORT is already in use by PID $LISTENER_PID."
    echo "Stop that process first (for example, Ctrl-C in that terminal) and rerun this script."
    exit 1
  fi
fi

echo "Loading LaunchAgent..."
launchctl bootstrap "gui/$UID" "$PLIST_PATH"
launchctl enable "gui/$UID/$LABEL"
launchctl kickstart -k "gui/$UID/$LABEL"

echo
echo "USB Vault launchd service installed and running."
echo "URL: http://127.0.0.1:$PORT"
echo "Status: launchctl print gui/$UID/$LABEL"
echo "Stop: ./scripts/macos/uninstall_launchd.sh --keep-plist"
echo "Logs: tail -f \"$STDOUT_LOG\" \"$STDERR_LOG\""
