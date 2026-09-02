# Video Drop Converter

This macOS service watches `/Volumes/Video2/Convert to MP4` for non-MP4 video
files. After a file has stopped changing for 15 seconds, it:

1. converts the first video stream and all audio streams to H.264/AAC MP4 using
   Apple's VideoToolbox hardware encoder;
2. probes the result, compares its duration, and fully decodes it to detect errors;
3. searches other mounted drives for same-size candidates and confirms the true
   source is byte-identical using SHA-256, even when its filename differs;
4. moves the verified MP4 to `/Volumes/Video2/Processed`;
5. renames the true original in place on its source drive to the matching
   sequential name; and
6. preserves the intake copy in the drop folder under that same name.

Names are zero-padded for Finder sorting, for example:

- source-drive original: `processed000001.mov`
- intake copy: `processed000001.mov`
- verified copy: `Processed/processed000001.mp4`

Files already ending in `.mp4`, hidden files, folders, and preserved sources named
`processedNNNNNN.*` are ignored. A file that fails source matching, probing,
conversion, or verification is left under its original name and retried after 30
seconds. Nothing is renamed unless exactly one byte-identical mounted-drive source
is found. Errors are recorded in the log.

Each verified input SHA-256 is recorded in `/Volumes/Video2/.video-drop-index.json`.
If identical bytes are dropped again under any filename, the existing verified MP4
is integrity-checked and reused instead of re-encoded. The new original and intake
copy reuse the existing number. When that name already exists in the same folder,
the preserved copy receives `-duplicate001`, `-duplicate002`, and so on.

The service runs through a small signed local app wrapper so macOS can grant only
removable-volume access to this converter. It is installed as the per-user
LaunchAgent `com.localplanner.video-drop-converter`. Its log is at
`~/Library/Logs/VideoDropConverter/video-drop-converter.log`.
