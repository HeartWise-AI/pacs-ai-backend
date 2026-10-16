#!/usr/bin/env python3
"""Download and verify the immutable PanEcho v1.0 artifacts."""

from __future__ import annotations

import argparse
import hashlib
import shutil
import urllib.request
from pathlib import Path


RELEASE_TAG = "v1.0"
EXPECTED_REPOSITORY = "CarDS-Yale/PanEcho"
EXPECTED_REVISION = "32561e471cd8f181550a659d9aa01146c9f9e322"
EXPECTED_WEIGHTS_PATH = "panecho.pt"
EXPECTED_TASKS_PATH = "content/tasks.pkl"
EXPECTED_TASKS_SHA256 = (
    "8b00f13d3739c59baf0da5c9a09a1a66ee9fc7a593852d6ef68ffac9a42435b0"
)
USER_AGENT = "PACS-AI-model-release/1.0"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as artifact:
        for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_file(path: Path, expected_sha256: str) -> Path:
    if not path.is_file():
        raise FileNotFoundError(f"Expected PanEcho artifact was not downloaded: {path}")
    actual_sha256 = sha256_file(path)
    if actual_sha256 != expected_sha256:
        raise ValueError(
            f"SHA-256 mismatch for {path.name}: "
            f"expected {expected_sha256}, received {actual_sha256}"
        )
    return path


def download(url: str, destination: Path, expected_sha256: str) -> Path:
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix(destination.suffix + ".part")
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    try:
        with urllib.request.urlopen(request) as response, temporary.open("wb") as output:
            shutil.copyfileobj(response, output, length=1024 * 1024)
        verify_file(temporary, expected_sha256)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)
    return destination


def verify_artifacts(
    output_dir: Path,
    weights_path: str,
    expected_weights_sha256: str,
) -> tuple[Path, Path]:
    weights = verify_file(output_dir / weights_path, expected_weights_sha256)
    tasks = verify_file(output_dir / "tasks.pkl", EXPECTED_TASKS_SHA256)
    return weights, tasks


def download_model(
    repository: str,
    revision: str,
    weights_path: str,
    expected_weights_sha256: str,
    output_dir: Path = Path("content"),
) -> tuple[Path, Path]:
    expected_identity = (EXPECTED_REPOSITORY, EXPECTED_REVISION, EXPECTED_WEIGHTS_PATH)
    if (repository, revision, weights_path) != expected_identity:
        raise ValueError("PanEcho release metadata does not identify the validated v1.0 artifacts")

    weights = download(
        f"https://github.com/{repository}/releases/download/{RELEASE_TAG}/{weights_path}",
        output_dir / weights_path,
        expected_weights_sha256,
    )
    tasks = download(
        f"https://raw.githubusercontent.com/{repository}/{revision}/{EXPECTED_TASKS_PATH}",
        output_dir / "tasks.pkl",
        EXPECTED_TASKS_SHA256,
    )
    print(f"Verified {repository}@{revision}:{weights_path} ({expected_weights_sha256})")
    return weights, tasks


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--weights-path", required=True)
    parser.add_argument("--weights-sha256", required=True)
    parser.add_argument("--output-dir", type=Path, default=Path("content"))
    parser.add_argument("--verify-only", action="store_true")
    args = parser.parse_args()

    if args.verify_only:
        verify_artifacts(args.output_dir, args.weights_path, args.weights_sha256)
        return

    download_model(
        args.repository,
        args.revision,
        args.weights_path,
        args.weights_sha256,
        args.output_dir,
    )


if __name__ == "__main__":
    main()
