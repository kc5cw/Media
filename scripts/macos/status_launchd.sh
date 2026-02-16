#!/usr/bin/env bash
set -euo pipefail

LABEL="com.kc5cw.usbvault"
STDOUT_LOG="$HOME/Library/Logs/USBVault/usbvault-launchd.out.log"
STDERR_LOG="$HOME/Library/Logs/USBVault/usbvault-launchd.err.log"

echo "LaunchAgent: $LABEL"
echo
launchctl print "gui/$UID/$LABEL" || true
echo
echo "Recent logs:"
tail -n 30 "$STDOUT_LOG" "$STDERR_LOG" 2>/dev/null || echo "No logs yet."
