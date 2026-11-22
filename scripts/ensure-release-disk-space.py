#!/usr/bin/env python3
"""Reclaim old, unused build cache when the dedicated release runner is full."""

import argparse
import shutil
import subprocess
from pathlib import Path

GIB = 1024 ** 3


def diagnose_storage(path: Path) -> None:
    """Report storage locations and usage without reading credential contents."""
    commands = [
        ["df", "-h", str(path), "/"],
        ["docker", "info", "--format", "DockerRootDir={{.DockerRootDir}} Driver={{.Driver}}"],
        ["docker", "system", "df"],
        ["du", "-x", "-h", "--max-depth=1", str(path.parent)],
    ]
    for command in commands:
        print("Storage diagnostic: " + " ".join(command), flush=True)
        try:
            subprocess.run(command, check=False, timeout=30)
        except (OSError, subprocess.TimeoutExpired) as exc:
            print(f"Storage diagnostic unavailable: {exc}", flush=True)


def ensure_space(path: Path, minimum_free_gib: int) -> None:
    if minimum_free_gib <= 0:
        raise ValueError("minimum free GiB must be positive")
    required = minimum_free_gib * GIB
    available = shutil.disk_usage(path).free
    print(f"Release filesystem: {available / GIB:.1f} GiB free; {minimum_free_gib} GiB required", flush=True)
    if available >= required:
        return
    print("Reclaiming unused Docker build cache older than 24 hours, retaining 10 GB of cache", flush=True)
    # No --all: preserve internal/frontend cache. Never prune images, containers,
    # volumes, or cache used by an active build. This runs only on the dedicated
    # approved model-release runner, and only when space is already insufficient.
    subprocess.run(
        ["docker", "builder", "prune", "--force", "--filter", "until=24h", "--keep-storage", "10GB"],
        check=True,
    )
    available = shutil.disk_usage(path).free
    print(f"Release filesystem after cache cleanup: {available / GIB:.1f} GiB free", flush=True)
    if available < required:
        raise RuntimeError(
            f"Insufficient runner disk space after bounded cache cleanup: "
            f"{available / GIB:.1f} GiB available, {minimum_free_gib} GiB required. "
            "Expand the filesystem or inspect its usage; no images, containers, or volumes were pruned."
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--path", type=Path, required=True)
    parser.add_argument("--minimum-free-gib", type=int, default=25)
    parser.add_argument("--diagnose", action="store_true")
    args = parser.parse_args()
    try:
        if args.diagnose:
            diagnose_storage(args.path)
        ensure_space(args.path, args.minimum_free_gib)
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as exc:
        parser.exit(1, f"{exc}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
