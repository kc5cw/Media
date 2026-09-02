#!/bin/sh
set -eu

source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
app_dir="/Users/curtishays/Library/Application Support/VideoDropConverter"
log_dir="/Users/curtishays/Library/Logs/VideoDropConverter"
agents_dir="/Users/curtishays/Library/LaunchAgents"
applications_dir="/Users/curtishays/Applications"
app_bundle="$applications_dir/Video Drop Converter.app"
watch_dir="/Volumes/Video2/Convert to MP4"
processed_dir="/Volumes/Video2/Processed"
desktop_link="/Users/curtishays/Desktop/Convert to MP4"
label="com.localplanner.video-drop-converter"

if [ ! -d /Volumes/Video2 ]; then
  echo "Video2 is not mounted at /Volumes/Video2" >&2
  exit 1
fi

if [ ! -d "$source_dir/build/Video Drop Converter.app" ]; then
  echo "Build the app first with $source_dir/build_app.sh" >&2
  exit 1
fi

/usr/bin/install -d -m 755 "$app_dir" "$log_dir" "$agents_dir" "$applications_dir" "$watch_dir" "$processed_dir"
/usr/bin/install -m 755 "$source_dir/video_drop_converter.py" "$app_dir/video_drop_converter.py"
/usr/bin/install -m 644 "$source_dir/com.localplanner.video-drop-converter.plist" "$agents_dir/$label.plist"
/usr/bin/ditto "$source_dir/build/Video Drop Converter.app" "$app_bundle"

if [ -L "$desktop_link" ]; then
  current_target=$(/usr/bin/readlink "$desktop_link")
  if [ "$current_target" != "$watch_dir" ]; then
    echo "Desktop link already exists and points to: $current_target" >&2
    exit 1
  fi
elif [ -e "$desktop_link" ]; then
  echo "Desktop item already exists and was not changed: $desktop_link" >&2
  exit 1
else
  /bin/ln -s "$watch_dir" "$desktop_link"
fi

/bin/launchctl bootout "gui/501/$label" 2>/dev/null || true
/bin/launchctl bootstrap gui/501 "$agents_dir/$label.plist"
/bin/launchctl kickstart "gui/501/$label"

echo "Installed $label"
echo "Drop folder: $watch_dir"
echo "Desktop link: $desktop_link"
echo "Processed folder: $processed_dir"
