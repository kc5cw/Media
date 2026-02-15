#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-$(date +%Y.%m.%d.%H%M)}"

"$ROOT_DIR/scripts/macos/build_app.sh" "$VERSION"
"$ROOT_DIR/scripts/macos/build_dmg.sh" "$VERSION"
"$ROOT_DIR/scripts/macos/sign_and_notarize.sh" "$VERSION"

echo "Release build complete for version $VERSION"
