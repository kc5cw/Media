#!/bin/sh
set -eu

source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
build_dir="$source_dir/build"
app_bundle="$build_dir/Video Drop Converter.app"

/bin/mkdir -p "$app_bundle/Contents/MacOS"
/usr/bin/install -m 644 "$source_dir/VideoDropConverter-Info.plist" "$app_bundle/Contents/Info.plist"
/usr/bin/xcrun swiftc \
  "$source_dir/VideoDropConverterRunner.swift" \
  -module-cache-path "$build_dir/module-cache" \
  -framework AppKit \
  -o "$app_bundle/Contents/MacOS/VideoDropConverter"
/usr/bin/codesign --force --deep --sign - "$app_bundle"
/usr/bin/plutil -lint "$app_bundle/Contents/Info.plist"

echo "Built $app_bundle"
