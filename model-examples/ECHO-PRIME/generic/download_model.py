#!/usr/bin/env python3
"""Download the public EchoPrime weights and PACS-AI auxiliary data.

The large binary artifacts intentionally stay out of Git.  This script pins
their upstream revisions and verifies every downloaded file before the Docker
image is assembled.
"""

from __future__ import annotations

import argparse
import hashlib
import shutil
import tempfile
import urllib.request
import zipfile
from pathlib import Path


ECHOPRIME_ARCHIVE_URL = (
    "https://github.com/echonet/EchoPrime/releases/download/v1.0.0/model_data.zip"
)
ECHOPRIME_ARCHIVE_SHA256 = (
    "b29362b6b40e8695b138d191f874ad92417b6da9fcdb2f0764ed1440be5272b3"
)

HF_REPOSITORY = "heartwise/pacs-ai-examples"
HF_REVISION = "fa50e0694834d191aa2bb1bb9f481a9d802ef4e2"
HF_AUXILIARY_BASE_URL = (
    f"https://huggingface.co/{HF_REPOSITORY}/resolve/{HF_REVISION}/"
    "ECHO-PRIME/models/auxiliary_files"
)

WEIGHT_FILES = {
    "model_data/weights/echo_prime_encoder.pt": (
        "echo_encoder.pt",
        "7ca32e8bfde248bd6d8c7e46fdb7440385169af4dc2f416b5de840bdc2e64f3b",
    ),
    "model_data/weights/view_classifier.pt": (
        "view_classifier.ckpt",
        "046b76685de5295d151f74725d69be758c53aac3a028132e3f1f160ba5143954",
    ),
}

AUXILIARY_FILES = {
    "MIL_weights.csv": "9f633461ee5b9e4bf2c6590cdf4cb5eba6f19c41fd9e36fd56fbb7913c8958e4",
    "all_phr.json": "875e72e9b474c2c5f7f22777d409ffcae9cf94b2a92aef84033cb6ffb70de944",
    "candidate_labels.pkl": "8225f16fd589613b32b71fb5e58f1c4ba75131baa50e437de51c43b912127e6d",
    "candidate_report_embeddings.pt": "c1135c6ea3ce24e9d7a629956c79373fbbb10562fd7321a9142480abf06a09eb",
    "candidate_reports.pkl": "ff31cf2e2ad30098c98b6c198193c0e03680405ab1ed36c06da3ef2cd7b75ef9",
    "candidate_studies.csv": "72a9fd5ea638b8c1cc5bcb485affd6b23b51b93bc16cc679475c1ef4f6ec7ee5",
    "per_section.json": "461b11865a101c010ca7c26af5b404733d2bfd51e0fc3499e34e123f05f07d87",
    "roc_thresholds.csv": "946b37823f9022d73628cddb4bd3efbeb79fe4a6e613d08632922be1aeb45fa3",
    "section_to_phenotypes.pkl": "e6ccace4a3e4446163de1f2aef541f73db4b2a926305a693f31d9007fc1666e8",
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify(path: Path, expected_sha256: str) -> None:
    actual_sha256 = sha256(path)
    if actual_sha256 != expected_sha256:
        raise RuntimeError(
            f"Checksum mismatch for {path.name}: "
            f"expected {expected_sha256}, got {actual_sha256}"
        )


def download(url: str, destination: Path, expected_sha256: str) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix(f"{destination.suffix}.part")
    request = urllib.request.Request(url, headers={"User-Agent": "pacs-ai-model-build"})
    try:
        with urllib.request.urlopen(request) as response, temporary.open("wb") as output:
            shutil.copyfileobj(response, output, length=1024 * 1024)
        verify(temporary, expected_sha256)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)


def extract_weights(archive_path: Path, output_dir: Path) -> None:
    weights_dir = output_dir / "weights"
    weights_dir.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory(prefix=".weights-", dir=output_dir) as temporary_dir:
        temporary_weights_dir = Path(temporary_dir)
        with zipfile.ZipFile(archive_path) as archive:
            for member, (filename, expected_sha256) in WEIGHT_FILES.items():
                destination = temporary_weights_dir / filename
                with archive.open(member) as source, destination.open("wb") as output:
                    shutil.copyfileobj(source, output, length=1024 * 1024)
                verify(destination, expected_sha256)

        for filename, _ in WEIGHT_FILES.values():
            (temporary_weights_dir / filename).replace(weights_dir / filename)


def download_model(output_dir: Path) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="echoprime-") as temporary_dir:
        archive_path = Path(temporary_dir) / "model_data.zip"
        print("Downloading EchoPrime v1.0.0 checkpoint archive...")
        download(ECHOPRIME_ARCHIVE_URL, archive_path, ECHOPRIME_ARCHIVE_SHA256)
        extract_weights(archive_path, output_dir)

    auxiliary_dir = output_dir / "auxiliary_files"
    for filename, expected_sha256 in AUXILIARY_FILES.items():
        print(f"Downloading EchoPrime auxiliary file {filename}...")
        download(
            f"{HF_AUXILIARY_BASE_URL}/{filename}",
            auxiliary_dir / filename,
            expected_sha256,
        )

    print("EchoPrime model artifacts downloaded and verified.")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=Path(__file__).resolve().parent / "models",
    )
    args = parser.parse_args()
    download_model(args.output_dir)


if __name__ == "__main__":
    main()
