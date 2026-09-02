#!/usr/bin/env python3
"""Register an already verified source/intake/MP4 set in the duplicate index."""

from __future__ import annotations

import argparse
from pathlib import Path

from video_drop_converter import record_index_item, sha256


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--number", type=int, required=True)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--intake", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--processed-dir", type=Path, required=True)
    args = parser.parse_args()

    source_digest = sha256(args.source)
    intake_digest = sha256(args.intake)
    if source_digest != intake_digest:
        raise ValueError("Source and intake hashes do not match")
    if args.output.parent.resolve() != args.processed_dir.resolve():
        raise ValueError("Output is not inside the specified processed directory")

    record_index_item(
        intake_digest,
        args.number,
        args.output.name,
        sha256(args.output),
        args.source,
        args.intake,
        args.processed_dir,
    )
    print(f"Registered input SHA-256 {intake_digest} as processed{args.number:06d}")


if __name__ == "__main__":
    main()
