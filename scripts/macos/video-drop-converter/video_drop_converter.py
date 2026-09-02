#!/usr/bin/env python3
"""Watch a folder, convert non-MP4 videos, verify them, and archive originals."""

from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import logging
import os
import re
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Any


ARCHIVED_NAME = re.compile(
    r"^processed(\d+)(?:-duplicate\d+)?(?:\.[^.]+)?$", re.IGNORECASE
)
INDEX_VERSION = 1
LOG = logging.getLogger("video-drop-converter")
STOP_REQUESTED = False


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--watch-dir", type=Path, required=True)
    parser.add_argument("--processed-dir", type=Path, required=True)
    parser.add_argument("--ffmpeg", default="/opt/homebrew/bin/ffmpeg")
    parser.add_argument("--ffprobe", default="/opt/homebrew/bin/ffprobe")
    parser.add_argument("--poll-seconds", type=float, default=5.0)
    parser.add_argument("--settle-seconds", type=float, default=15.0)
    parser.add_argument("--source-search-root", type=Path, default=Path("/Volumes"))
    parser.add_argument("--once", action="store_true", help="Process stable files once and exit")
    return parser.parse_args()


def handle_stop(_signum: int, _frame: object) -> None:
    global STOP_REQUESTED
    STOP_REQUESTED = True


def run(command: list[str], *, capture: bool = False) -> subprocess.CompletedProcess[str]:
    LOG.debug("Running: %s", " ".join(command))
    return subprocess.run(
        command,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
        stderr=subprocess.PIPE if capture else None,
    )


def probe(path: Path, ffprobe: str) -> dict[str, Any]:
    result = run(
        [
            ffprobe,
            "-v",
            "error",
            "-show_entries",
            "format=duration:stream=index,codec_type,duration",
            "-of",
            "json",
            str(path),
        ],
        capture=True,
    )
    return json.loads(result.stdout)


def duration_seconds(info: dict[str, Any]) -> float | None:
    raw = info.get("format", {}).get("duration")
    try:
        return float(raw) if raw is not None else None
    except (TypeError, ValueError):
        return None


def assert_video(info: dict[str, Any], path: Path) -> None:
    if not any(stream.get("codec_type") == "video" for stream in info.get("streams", [])):
        raise ValueError(f"No video stream found in {path.name}")


def verify_output(source_info: dict[str, Any], output: Path, ffmpeg: str, ffprobe: str) -> None:
    if not output.is_file() or output.stat().st_size == 0:
        raise ValueError("Converted file is missing or empty")

    output_info = probe(output, ffprobe)
    assert_video(output_info, output)

    source_duration = duration_seconds(source_info)
    output_duration = duration_seconds(output_info)
    if source_duration and output_duration:
        tolerance = max(2.0, source_duration * 0.02)
        if abs(source_duration - output_duration) > tolerance:
            raise ValueError(
                f"Duration mismatch: source={source_duration:.3f}s output={output_duration:.3f}s"
            )

    # Decode the complete output. Any corrupt packet or undecodable frame fails verification.
    run(
        [
            ffmpeg,
            "-v",
            "error",
            "-xerror",
            "-i",
            str(output),
            "-map",
            "0:v:0",
            "-map",
            "0:a?",
            "-f",
            "null",
            "-",
        ]
    )


def sequence_roots(watch_dir: Path, processed_dir: Path) -> list[Path]:
    roots = [watch_dir, processed_dir]
    volume_root = Path(watch_dir.anchor) / "Volumes" / watch_dir.parts[2] if len(watch_dir.parts) > 2 and watch_dir.parts[1] == "Volumes" else None
    if volume_root and volume_root.is_dir():
        roots.append(volume_root)
    return roots


def containing_volume(search_root: Path, path: Path) -> Path | None:
    try:
        relative = path.resolve().relative_to(search_root.resolve())
    except ValueError:
        return None
    return search_root.resolve() / relative.parts[0] if relative.parts else None


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while chunk := handle.read(8 * 1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def find_source_original(
    intake: Path, intake_digest: str, search_root: Path, watch_dir: Path
) -> Path:
    """Find one byte-identical source file on another mounted volume."""
    excluded_volume = containing_volume(search_root, watch_dir)
    intake_stat = intake.stat()
    candidates: list[tuple[Path, int]] = []

    try:
        volumes = list(search_root.iterdir())
    except FileNotFoundError:
        volumes = []

    for volume in volumes:
        try:
            resolved = volume.resolve()
        except OSError:
            continue
        if volume.is_symlink() or not volume.is_dir() or resolved == excluded_volume:
            continue
        for current_root, dirs, files in os.walk(volume, onerror=lambda _error: None):
            dirs[:] = [
                name
                for name in dirs
                if not name.startswith(".") and name != "System Volume Information"
            ]
            for name in files:
                if name.startswith("."):
                    continue
                candidate = Path(current_root) / name
                try:
                    candidate_stat = candidate.stat()
                except OSError:
                    continue
                if candidate_stat.st_size != intake_stat.st_size:
                    continue
                score = 0
                if candidate.name == intake.name:
                    score += 8
                if candidate_stat.st_mtime_ns == intake_stat.st_mtime_ns:
                    score += 4
                if candidate.suffix.casefold() == intake.suffix.casefold():
                    score += 2
                if not ARCHIVED_NAME.match(candidate.name):
                    score += 1
                candidates.append((candidate, score))

    if not candidates:
        raise ValueError(f"No same-name source found for {intake.name} on another mounted drive")

    exact_matches: list[tuple[Path, int]] = []
    for candidate, score in candidates:
        try:
            if sha256(candidate) == intake_digest:
                exact_matches.append((candidate, score))
        except OSError:
            continue

    if not exact_matches:
        raise ValueError(
            f"Source copy for {intake.name} is incomplete or differs from the mounted-drive original"
        )
    highest_score = max(score for _path, score in exact_matches)
    best_matches = [path for path, score in exact_matches if score == highest_score]
    if len(best_matches) > 1:
        locations = ", ".join(str(path) for path in best_matches)
        raise ValueError(f"Multiple identical source originals found for {intake.name}: {locations}")

    LOG.info("Matched intake %s to source original %s", intake.name, best_matches[0])
    return best_matches[0]


def index_paths(processed_dir: Path) -> tuple[Path, Path]:
    volume_root = processed_dir.parent
    return volume_root / ".video-drop-index.json", volume_root / ".video-drop-index.lock"


def load_index(index_path: Path) -> dict[str, Any]:
    try:
        data = json.loads(index_path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return {"version": INDEX_VERSION, "items": {}}
    if data.get("version") != INDEX_VERSION or not isinstance(data.get("items"), dict):
        raise ValueError(f"Unsupported or invalid duplicate index: {index_path}")
    return data


def write_index(index_path: Path, data: dict[str, Any]) -> None:
    temporary = index_path.with_name(f"{index_path.name}.{os.getpid()}.tmp")
    temporary.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(temporary, index_path)


def lookup_duplicate(intake_digest: str, processed_dir: Path) -> dict[str, Any] | None:
    index_path, lock_path = index_paths(processed_dir)
    with lock_path.open("a+", encoding="utf-8") as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        try:
            item = load_index(index_path)["items"].get(intake_digest)
            if item is None:
                return None
            item = dict(item)
        finally:
            fcntl.flock(lock.fileno(), fcntl.LOCK_UN)

    output = processed_dir / str(item.get("output_name", ""))
    expected_digest = item.get("output_sha256")
    if not output.is_file() or not expected_digest:
        LOG.warning("Duplicate index entry has no valid output for input digest %s", intake_digest)
        return None
    if sha256(output) != expected_digest:
        LOG.warning("Indexed output failed SHA-256 integrity check: %s", output)
        return None
    return item


def record_index_item(
    intake_digest: str,
    number: int,
    output_name: str,
    output_digest: str,
    source_path: Path,
    intake_path: Path,
    processed_dir: Path,
) -> None:
    index_path, lock_path = index_paths(processed_dir)
    with lock_path.open("a+", encoding="utf-8") as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        try:
            data = load_index(index_path)
            item = dict(data["items"].get(intake_digest, {}))
            item.update(
                {
                    "number": number,
                    "output_name": output_name,
                    "output_sha256": output_digest,
                    "verified_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                }
            )
            sources = list(item.get("sources", []))
            intakes = list(item.get("intakes", []))
            if str(source_path) not in sources:
                sources.append(str(source_path))
            if str(intake_path) not in intakes:
                intakes.append(str(intake_path))
            item["sources"] = sources
            item["intakes"] = intakes
            data["items"][intake_digest] = item
            write_index(index_path, data)
        finally:
            fcntl.flock(lock.fileno(), fcntl.LOCK_UN)


def numbered_path(original: Path, stem: str) -> Path:
    suffix = original.suffix.lower()
    base = original.with_name(f"{stem}{suffix}")
    if original == base or not base.exists():
        return base
    for duplicate_number in range(1, 1_000_000):
        candidate = original.with_name(f"{stem}-duplicate{duplicate_number:03d}{suffix}")
        if original == candidate or not candidate.exists():
            return candidate
    raise RuntimeError(f"Too many duplicate names beside {original}")


def archive_duplicate(
    source: Path,
    source_original: Path,
    intake_digest: str,
    duplicate: dict[str, Any],
    args: argparse.Namespace,
) -> None:
    number = int(duplicate["number"])
    stem = f"processed{number:06d}"
    renamed_original = numbered_path(source_original, stem)
    archived_intake = numbered_path(source, stem)
    original_moved = renamed_original != source_original

    if original_moved:
        source_original.rename(renamed_original)
    try:
        source.rename(archived_intake)
        try:
            record_index_item(
                intake_digest,
                number,
                str(duplicate["output_name"]),
                str(duplicate["output_sha256"]),
                renamed_original,
                archived_intake,
                args.processed_dir,
            )
        except Exception:
            archived_intake.rename(source)
            raise
    except Exception:
        if original_moved:
            renamed_original.rename(source_original)
        raise

    LOG.info(
        "Duplicate %s reused verified %s; source renamed to %s; intake archived as %s",
        source.name,
        duplicate["output_name"],
        renamed_original,
        archived_intake.name,
    )


def highest_existing_sequence(watch_dir: Path, processed_dir: Path) -> int:
    """Find the highest workflow number on the mounted volume.

    A full-volume scan is done only when initializing the sequence ledger. Future runs
    use the ledger, while collision checks still protect both workflow folders.
    """
    maximum = 0
    seen: set[Path] = set()
    for root in sequence_roots(watch_dir, processed_dir):
        root = root.resolve()
        if root in seen or not root.exists():
            continue
        seen.add(root)
        for current_root, dirs, files in os.walk(root):
            dirs[:] = [name for name in dirs if not name.startswith(".")]
            for name in files:
                match = ARCHIVED_NAME.match(name)
                if match:
                    maximum = max(maximum, int(match.group(1)))
    return maximum


def reserve_sequence(watch_dir: Path, processed_dir: Path, source_original: Path) -> int:
    volume_root = processed_dir.parent
    ledger = volume_root / ".video-drop-next-sequence"
    lock_path = volume_root / ".video-drop-sequence.lock"

    with lock_path.open("a+", encoding="utf-8") as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        try:
            try:
                candidate = int(ledger.read_text(encoding="utf-8").strip())
            except (FileNotFoundError, ValueError):
                candidate = highest_existing_sequence(watch_dir, processed_dir) + 1

            while (
                any(watch_dir.glob(f"processed{candidate:06d}.*"))
                or (processed_dir / f"processed{candidate:06d}.mp4").exists()
                or source_original.with_name(
                    f"processed{candidate:06d}{source_original.suffix.lower()}"
                ).exists()
            ):
                candidate += 1

            temporary = ledger.with_suffix(".tmp")
            temporary.write_text(f"{candidate + 1}\n", encoding="utf-8")
            os.replace(temporary, ledger)
            return candidate
        finally:
            fcntl.flock(lock.fileno(), fcntl.LOCK_UN)


def convert_one(source: Path, args: argparse.Namespace) -> None:
    source_info = probe(source, args.ffprobe)
    assert_video(source_info, source)
    intake_digest = sha256(source)
    source_original = find_source_original(
        source, intake_digest, args.source_search_root, args.watch_dir
    )
    duplicate = lookup_duplicate(intake_digest, args.processed_dir)
    if duplicate is not None:
        archive_duplicate(source, source_original, intake_digest, duplicate, args)
        return

    number = reserve_sequence(args.watch_dir, args.processed_dir, source_original)
    stem = f"processed{number:06d}"
    final_output = args.processed_dir / f"{stem}.mp4"
    archived_intake = args.watch_dir / f"{stem}{source.suffix.lower()}"
    renamed_original = source_original.with_name(f"{stem}{source_original.suffix.lower()}")
    work_dir = args.processed_dir / ".video-drop-work"
    work_dir.mkdir(parents=True, exist_ok=True)
    partial_output = work_dir / f".{stem}.{os.getpid()}.partial.mp4"

    LOG.info("Converting %s -> %s", source.name, final_output.name)
    try:
        run(
            [
                args.ffmpeg,
                "-hide_banner",
                "-nostdin",
                "-y",
                "-i",
                str(source),
                "-map",
                "0:v:0",
                "-map",
                "0:a?",
                "-map_metadata",
                "0",
                "-vf",
                "scale=trunc(iw/2)*2:trunc(ih/2)*2",
                "-c:v",
                "h264_videotoolbox",
                "-profile:v",
                "high",
                "-q:v",
                "65",
                "-prio_speed",
                "1",
                "-pix_fmt",
                "yuv420p",
                "-c:a",
                "aac",
                "-b:a",
                "192k",
                "-movflags",
                "+faststart",
                str(partial_output),
            ]
        )
        verify_output(source_info, partial_output, args.ffmpeg, args.ffprobe)
        output_digest = sha256(partial_output)

        # Rename the true mounted-drive original only after verification. Roll all
        # names back if archiving the intake or delivering the MP4 unexpectedly fails.
        source_original.rename(renamed_original)
        try:
            source.rename(archived_intake)
            try:
                os.replace(partial_output, final_output)
                try:
                    record_index_item(
                        intake_digest,
                        number,
                        final_output.name,
                        output_digest,
                        renamed_original,
                        archived_intake,
                        args.processed_dir,
                    )
                except Exception:
                    os.replace(final_output, partial_output)
                    raise
            except Exception:
                archived_intake.rename(source)
                raise
        except Exception:
            renamed_original.rename(source_original)
            raise
        LOG.info(
            "Verified %s; mounted-drive original renamed to %s; intake archived as %s",
            final_output.name,
            renamed_original,
            archived_intake.name,
        )
    finally:
        partial_output.unlink(missing_ok=True)


def eligible_files(watch_dir: Path) -> list[Path]:
    try:
        entries = list(watch_dir.iterdir())
    except FileNotFoundError:
        return []
    return sorted(
        (
            path
            for path in entries
            if path.is_file()
            and not path.name.startswith(".")
            and path.suffix.casefold() != ".mp4"
            and not ARCHIVED_NAME.match(path.name)
        ),
        key=lambda path: path.name.casefold(),
    )


def ensure_ready(args: argparse.Namespace) -> None:
    if shutil.which(args.ffmpeg) is None:
        raise FileNotFoundError(f"ffmpeg not found: {args.ffmpeg}")
    if shutil.which(args.ffprobe) is None:
        raise FileNotFoundError(f"ffprobe not found: {args.ffprobe}")
    args.watch_dir.mkdir(parents=True, exist_ok=True)
    args.processed_dir.mkdir(parents=True, exist_ok=True)


def main() -> int:
    args = parse_args()
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    signal.signal(signal.SIGTERM, handle_stop)
    signal.signal(signal.SIGINT, handle_stop)
    states: dict[Path, tuple[int, int, float]] = {}
    failures: dict[Path, tuple[int, int, float]] = {}
    original_parent_pid = os.getppid()

    while not STOP_REQUESTED:
        # The signed app wrapper owns this worker. If the wrapper is force-killed,
        # stop instead of becoming an orphaned second watcher under launchd.
        if original_parent_pid != 1 and os.getppid() == 1:
            LOG.info("App wrapper exited; stopping orphaned worker")
            return 0
        try:
            ensure_ready(args)
            current = eligible_files(args.watch_dir)
            current_set = set(current)
            states = {path: state for path, state in states.items() if path in current_set}
            failures = {path: state for path, state in failures.items() if path in current_set}

            for path in current:
                try:
                    stat = path.stat()
                except FileNotFoundError:
                    continue
                signature = (stat.st_size, stat.st_mtime_ns)
                previous = states.get(path)
                if previous is None or previous[:2] != signature:
                    states[path] = (*signature, time.monotonic())
                    failures.pop(path, None)
                    if not args.once:
                        continue
                    previous = (*signature, time.monotonic() - args.settle_seconds)
                if not args.once and time.monotonic() - previous[2] < args.settle_seconds:
                    continue
                failure = failures.get(path)
                if failure and failure[:2] == signature and time.monotonic() < failure[2]:
                    continue
                try:
                    convert_one(path, args)
                    states.pop(path, None)
                except Exception as exc:
                    failures[path] = (*signature, time.monotonic() + 30.0)
                    LOG.error("Could not process %s: %s", path.name, exc)
            if args.once:
                return 0
        except Exception as exc:
            LOG.error("Watcher error (will retry): %s", exc)

        time.sleep(max(args.poll_seconds, 1.0))
    return 0


if __name__ == "__main__":
    sys.exit(main())
