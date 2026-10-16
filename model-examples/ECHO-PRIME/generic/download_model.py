#!/usr/bin/env python3
"""Download and verify the immutable EchoPrime v1.0.0 model artifacts."""

from __future__ import annotations

import argparse
import hashlib
import shutil
import tempfile
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath


RELEASE_TAG = "v1.0.0"
EXPECTED_REPOSITORY = "echonet/EchoPrime"
EXPECTED_REVISION = "a964d9c92fb1aff7e50b218b15e7ba53ee37676d"
EXPECTED_WEIGHTS_PATH = "model_data/weights/echo_prime_encoder.pt"
EXPECTED_ARCHIVE_SHA256 = (
    "b29362b6b40e8695b138d191f874ad92417b6da9fcdb2f0764ed1440be5272b3"
)
USER_AGENT = "PACS-AI-model-release/1.0"

HF_REPOSITORY = "heartwise/pacs-ai-examples"
HF_REVISION = "fa50e0694834d191aa2bb1bb9f481a9d802ef4e2"
HF_AUXILIARY_BASE_URL = (
    f"https://huggingface.co/{HF_REPOSITORY}/resolve/{HF_REVISION}/"
    "ECHO-PRIME/models/auxiliary_files"
)

WEIGHT_FILES = {
    EXPECTED_WEIGHTS_PATH: (
        "7ca32e8bfde248bd6d8c7e46fdb7440385169af4dc2f416b5de840bdc2e64f3b"
    ),
    "model_data/weights/view_classifier.pt": (
        "046b76685de5295d151f74725d69be758c53aac3a028132e3f1f160ba5143954"
    ),
}

AUXILIARY_FILES = {
    "MIL_weights.csv": "9f633461ee5b9e4bf2c6590cdf4cb5eba6f19c41fd9e36fd56fbb7913c8958e4",
    "all_phr.json": "875e72e9b474c2c5f7f22777d409ffcae9cf94b2a92aef84033cb6ffb70de944",
    "candidate_labels.pkl": "8225f16fd589613b32b71fb5e58f1c4ba75131baa50e437de51c43b912127e6d",
    "candidate_report_embeddings.pt": (
        "c1135c6ea3ce24e9d7a629956c79373fbbb10562fd7321a9142480abf06a09eb"
    ),
    "candidate_reports.pkl": "ff31cf2e2ad30098c98b6c198193c0e03680405ab1ed36c06da3ef2cd7b75ef9",
    "candidate_studies.csv": "72a9fd5ea638b8c1cc5bcb485affd6b23b51b93bc16cc679475c1ef4f6ec7ee5",
    "per_section.json": "461b11865a101c010ca7c26af5b404733d2bfd51e0fc3499e34e123f05f07d87",
    "roc_thresholds.csv": "946b37823f9022d73628cddb4bd3efbeb79fe4a6e613d08632922be1aeb45fa3",
    "section_to_phenotypes.pkl": (
        "e6ccace4a3e4446163de1f2aef541f73db4b2a926305a693f31d9007fc1666e8"
    ),
}


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as artifact:
        for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_file(path: Path, expected_sha256: str) -> Path:
    if not path.is_file():
        raise FileNotFoundError(f"Expected EchoPrime artifact was not downloaded: {path}")
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


def extract_weights(archive_path: Path, output_dir: Path) -> None:
    with zipfile.ZipFile(archive_path) as archive:
        members = set(archive.namelist())
        for member, expected_sha256 in WEIGHT_FILES.items():
            member_path = PurePosixPath(member)
            if member_path.is_absolute() or ".." in member_path.parts:
                raise ValueError(f"Unsafe path in EchoPrime archive: {member}")
            if member not in members:
                raise FileNotFoundError(f"Expected EchoPrime checkpoint is absent: {member}")
            destination = output_dir / member_path
            destination.parent.mkdir(parents=True, exist_ok=True)
            temporary = destination.with_suffix(destination.suffix + ".part")
            try:
                with archive.open(member) as source, temporary.open("wb") as output:
                    shutil.copyfileobj(source, output, length=1024 * 1024)
                verify_file(temporary, expected_sha256)
                temporary.replace(destination)
            finally:
                temporary.unlink(missing_ok=True)


def verify_artifacts(
    output_dir: Path,
    weights_path: str,
    expected_weights_sha256: str,
) -> Path:
    primary = verify_file(output_dir / weights_path, expected_weights_sha256)
    for relative_path, expected_sha256 in WEIGHT_FILES.items():
        verify_file(output_dir / relative_path, expected_sha256)
    for filename, expected_sha256 in AUXILIARY_FILES.items():
        verify_file(output_dir / "auxiliary_files" / filename, expected_sha256)
    return primary


def download_model(
    repository: str,
    revision: str,
    weights_path: str,
    expected_weights_sha256: str,
    output_dir: Path = Path("models"),
) -> Path:
    expected_identity = (EXPECTED_REPOSITORY, EXPECTED_REVISION, EXPECTED_WEIGHTS_PATH)
    if (repository, revision, weights_path) != expected_identity:
        raise ValueError("EchoPrime release metadata does not identify the validated v1.0.0 encoder")
    if expected_weights_sha256 != WEIGHT_FILES[EXPECTED_WEIGHTS_PATH]:
        raise ValueError("EchoPrime encoder SHA-256 does not match the validated checkpoint")

    output_dir.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="echoprime-") as temporary_dir:
        archive_path = Path(temporary_dir) / "model_data.zip"
        download(
            f"https://github.com/{repository}/releases/download/{RELEASE_TAG}/model_data.zip",
            archive_path,
            EXPECTED_ARCHIVE_SHA256,
        )
        extract_weights(archive_path, output_dir)

    auxiliary_dir = output_dir / "auxiliary_files"
    for filename, expected_sha256 in AUXILIARY_FILES.items():
        download(
            f"{HF_AUXILIARY_BASE_URL}/{filename}",
            auxiliary_dir / filename,
            expected_sha256,
        )

    primary = verify_artifacts(output_dir, weights_path, expected_weights_sha256)
    print(f"Verified {repository}@{revision}:{weights_path} ({expected_weights_sha256})")
    return primary


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--weights-path", required=True)
    parser.add_argument("--weights-sha256", required=True)
    parser.add_argument("--output-dir", type=Path, default=Path("models"))
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
