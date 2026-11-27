#!/usr/bin/env python3
"""Download and verify the immutable EchoAlign artifacts from Hugging Face (pinned revision, sha256).

EchoAlign weights and probes: huggingface.co/EchoAlign/EchoAlign, CC BY-NC 4.0, research use only.
Nothing is committed to git; the Docker build fetches and verifies every file.
"""
from __future__ import annotations

import argparse
import hashlib
import shutil
import urllib.request
from pathlib import Path

EXPECTED_REPOSITORY = "EchoAlign/EchoAlign"
EXPECTED_REVISION = "7ceeb3e3e3dc342b4e36ecf0312a50edb120adde"
EXPECTED_WEIGHTS_PATH = "EchoAlign.pt"
# Companion files of the same revision: released linear probes, prompts and grade names.
COMPANION_FILES = {
    "heads/echoalign_private_heads.npz": "413eaa9febee0eabd745a680b535896a1bcc6a6e15538655406f726648dd6893",
    "heads/echoalign_private_heads.json": "dfefee970d39ac85bed77dd70796d0050a6ce2c582657c43d7163c8aab3ed628",
    "prompts.yaml": "e06a09ddc72d4bc05580044aa62f07fd01ac2c4f5ef8a1c11b8950aedc9900fd",
    "grades.yaml": "d13cf8477eb9733cd1bb3eacf60179c0f73c8a089129d51c2804a405016863c2",
}
USER_AGENT = "PACS-AI-model-release/1.0"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as artifact:
        for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_file(path: Path, expected_sha256: str) -> Path:
    if not path.is_file():
        raise FileNotFoundError(f"Expected EchoAlign artifact was not downloaded: {path}")
    actual = sha256_file(path)
    if actual != expected_sha256:
        raise ValueError(f"SHA-256 mismatch for {path.name}: expected {expected_sha256}, received {actual}")
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


def resolve_url(repository: str, revision: str, path: str) -> str:
    return f"https://huggingface.co/{repository}/resolve/{revision}/{path}"


def download_model(repository: str, revision: str, weights_path: str, weights_sha256: str,
                   output_dir: Path = Path("models")) -> list[Path]:
    if (repository, revision, weights_path) != (EXPECTED_REPOSITORY, EXPECTED_REVISION, EXPECTED_WEIGHTS_PATH):
        raise ValueError("EchoAlign release metadata does not identify the validated artifacts")
    files = [download(resolve_url(repository, revision, weights_path), output_dir / Path(weights_path).name, weights_sha256)]
    for path, sha in COMPANION_FILES.items():
        files.append(download(resolve_url(repository, revision, path), output_dir / Path(path).name, sha))
    print(f"Verified {repository}@{revision}:{weights_path} ({weights_sha256}) and {len(COMPANION_FILES)} companion files")
    return files


def verify_artifacts(output_dir: Path, weights_path: str, weights_sha256: str) -> list[Path]:
    files = [verify_file(output_dir / Path(weights_path).name, weights_sha256)]
    files += [verify_file(output_dir / Path(path).name, sha) for path, sha in COMPANION_FILES.items()]
    return files


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--weights-path", required=True)
    parser.add_argument("--weights-sha256", required=True)
    parser.add_argument("--output-dir", type=Path, default=Path("models"))
    parser.add_argument("--verify-only", action="store_true", help="Only verify the packaged artifacts")
    args = parser.parse_args()
    if args.verify_only:
        verify_artifacts(args.output_dir, args.weights_path, args.weights_sha256)
        print("Verified packaged EchoAlign artifacts")
        return
    download_model(args.repository, args.revision, args.weights_path, args.weights_sha256, args.output_dir)


if __name__ == "__main__":
    main()
