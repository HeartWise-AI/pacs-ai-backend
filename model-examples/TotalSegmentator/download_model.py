import argparse
import hashlib
import shutil
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath


WEIGHTS_TAG = "v2.0.0-weights"
EXPECTED_REPOSITORY = "wasserth/TotalSegmentator"
EXPECTED_REVISION = "d0eb302278588eade4dcc33093d141fe57a3ca90"
EXPECTED_WEIGHTS_PATH = "Dataset297_TotalSegmentator_total_3mm_1559subj.zip"
EXTRACTED_DATASET = "Dataset297_TotalSegmentator_total_3mm_1559subj"
EXPECTED_CHECKPOINT_SHA256 = "1e38e40356adc2706a662e365405a97f862d62a7da65f6bf81d025aee1b979ac"
DEFAULT_INSTALL_DIR = Path("/root/.totalsegmentator/nnunet/results")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as artifact:
        for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_weights(output_dir: Path, weights_path: str, expected_sha256: str) -> Path:
    archive = output_dir / weights_path
    if not archive.is_file():
        raise FileNotFoundError(f"Expected weight archive was not downloaded: {archive}")

    actual_sha256 = sha256_file(archive)
    if actual_sha256 != expected_sha256:
        raise ValueError(
            f"Weight archive SHA-256 mismatch for {archive}: "
            f"expected {expected_sha256}, received {actual_sha256}"
        )
    return archive


def safe_extract(archive: Path, install_dir: Path) -> None:
    install_dir.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(archive) as bundle:
        for member in bundle.infolist():
            path = PurePosixPath(member.filename)
            if path.is_absolute() or ".." in path.parts:
                raise ValueError(f"Unsafe path in weight archive: {member.filename}")
        bundle.extractall(install_dir)

    checkpoint = install_dir / EXTRACTED_DATASET / (
        "nnUNetTrainer_4000epochs_NoMirroring__nnUNetPlans__3d_fullres/"
        "fold_0/checkpoint_final.pth"
    )
    if not checkpoint.is_file():
        raise FileNotFoundError(f"Expected task-297 checkpoint was not extracted: {checkpoint}")
    actual_checkpoint_sha256 = sha256_file(checkpoint)
    if actual_checkpoint_sha256 != EXPECTED_CHECKPOINT_SHA256:
        raise ValueError(
            f"Task-297 checkpoint SHA-256 mismatch: expected "
            f"{EXPECTED_CHECKPOINT_SHA256}, received {actual_checkpoint_sha256}"
        )


def download_model(
    repository: str,
    revision: str,
    weights_path: str,
    expected_sha256: str,
    output_dir: Path = Path("models"),
    install_dir: Path = DEFAULT_INSTALL_DIR,
) -> Path:
    expected_identity = (EXPECTED_REPOSITORY, EXPECTED_REVISION, EXPECTED_WEIGHTS_PATH)
    if (repository, revision, weights_path) != expected_identity:
        raise ValueError(
            "TotalSegmentator release metadata does not identify the validated task-297 artifact"
        )

    output_dir.mkdir(parents=True, exist_ok=True)
    archive = output_dir / weights_path
    url = f"https://github.com/{repository}/releases/download/{WEIGHTS_TAG}/{weights_path}"
    temporary = archive.with_suffix(archive.suffix + ".part")

    try:
        with urllib.request.urlopen(url) as response, temporary.open("wb") as destination:
            shutil.copyfileobj(response, destination)
        temporary.replace(archive)
    finally:
        temporary.unlink(missing_ok=True)

    verify_weights(output_dir, weights_path, expected_sha256)
    safe_extract(archive, install_dir)
    print(f"Verified {repository}@{revision}:{weights_path} ({expected_sha256})")
    return archive


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--weights-path", required=True)
    parser.add_argument("--weights-sha256", required=True)
    parser.add_argument("--output-dir", type=Path, default=Path("models"))
    parser.add_argument("--install-dir", type=Path, default=DEFAULT_INSTALL_DIR)
    parser.add_argument("--verify-only", action="store_true")
    args = parser.parse_args()

    if args.verify_only:
        verify_weights(args.output_dir, args.weights_path, args.weights_sha256)
    else:
        download_model(
            args.repository,
            args.revision,
            args.weights_path,
            args.weights_sha256,
            args.output_dir,
            args.install_dir,
        )
