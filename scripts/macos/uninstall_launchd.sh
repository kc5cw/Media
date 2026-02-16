#!/usr/bin/env bash
set -euo pipefail

LABEL="com.kc5cw.usbvault"
PLIST_PATH="$HOME/Library/LaunchAgents/$LABEL.plist"
KEEP_PLIST=0

if [[ "${1:-}" == "--keep-plist" ]]; then
  KEEP_PLIST=1
fi

if launchctl print "gui/$UID/$LABEL" >/dev/null 2>&1; then
  echo "Stopping LaunchAgent $LABEL..."
  launchctl bootout "gui/$UID/$LABEL" || true
fi

launchctl disable "gui/$UID/$LABEL" >/dev/null 2>&1 || true

if [[ "$KEEP_PLIST" -eq 0 ]]; then
  if [[ -f "$PLIST_PATH" ]]; then
    rm -f "$PLIST_PATH"
    echo "Removed $PLIST_PATH"
  fi
else
  echo "Kept plist: $PLIST_PATH"
fi

echo "USB Vault launchd service stopped."
