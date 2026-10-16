import argparse
import hashlib
import shutil
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath


EXPECTED_REPOSITORY = "heartwise/hemorrhage"
EXPECTED_REVISION = "84d02ffe230ca3e648c6e4ca00a6affb984d4ed9"
EXPECTED_WEIGHTS_PATH = "Dataset005_WinMultiICHv5.zip"
EXPECTED_CHECKPOINT_SHA256 = "8a37ae99f60a4a6fac4716ef1ed95bee7931f59bc04c462e494ef39605b15f91"
CHECKPOINT_PATH = Path(
    "Dataset005_WinMultiICHv5/nnUNetTrainer__nnUNetPlans__3d_fullres/"
    "fold_0/checkpoint_final.pth"
)


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


def safe_extract(archive: Path, output_dir: Path) -> Path:
    with zipfile.ZipFile(archive) as bundle:
        for member in bundle.infolist():
            path = PurePosixPath(member.filename)
            if path.is_absolute() or ".." in path.parts:
                raise ValueError(f"Unsafe path in weight archive: {member.filename}")
        bundle.extractall(output_dir)

    checkpoint = output_dir / CHECKPOINT_PATH
    if not checkpoint.is_file():
        raise FileNotFoundError(f"Expected fold-0 checkpoint was not extracted: {checkpoint}")
    actual_checkpoint_sha256 = sha256_file(checkpoint)
    if actual_checkpoint_sha256 != EXPECTED_CHECKPOINT_SHA256:
        raise ValueError(
            f"Fold-0 checkpoint SHA-256 mismatch: expected {EXPECTED_CHECKPOINT_SHA256}, "
            f"received {actual_checkpoint_sha256}"
        )
    return checkpoint


def download_model(
    token: str,
    repository: str,
    revision: str,
    weights_path: str,
    expected_sha256: str,
    output_dir: Path = Path("models"),
) -> Path:
    expected_identity = (EXPECTED_REPOSITORY, EXPECTED_REVISION, EXPECTED_WEIGHTS_PATH)
    if (repository, revision, weights_path) != expected_identity:
        raise ValueError("Hemorrhage release metadata does not identify the validated model bundle")
    if not token:
        raise ValueError("A Hugging Face access token is required")

    output_dir.mkdir(parents=True, exist_ok=True)
    archive = output_dir / weights_path
    temporary = archive.with_suffix(archive.suffix + ".part")
    request = urllib.request.Request(
        f"https://huggingface.co/{repository}/resolve/{revision}/{weights_path}",
        headers={"Authorization": f"Bearer {token}"},
    )

    try:
        with urllib.request.urlopen(request) as response, temporary.open("wb") as destination:
            shutil.copyfileobj(response, destination)
        temporary.replace(archive)
    finally:
        temporary.unlink(missing_ok=True)

    verify_weights(output_dir, weights_path, expected_sha256)
    safe_extract(archive, output_dir)
    print(f"Verified {repository}@{revision}:{weights_path} ({expected_sha256})")
    return archive


def read_token(token_file: str | None) -> str:
    if token_file:
        return Path(token_file).read_text(encoding="utf-8").strip()
    raise ValueError("Pass --token-file")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--weights-path", required=True)
    parser.add_argument("--weights-sha256", required=True)
    parser.add_argument("--output-dir", type=Path, default=Path("models"))
    parser.add_argument("--verify-only", action="store_true")
    parser.add_argument("--token-file")
    args = parser.parse_args()

    if args.verify_only:
        verify_weights(args.output_dir, args.weights_path, args.weights_sha256)
    else:
        download_model(
            read_token(args.token_file),
            args.repository,
            args.revision,
            args.weights_path,
            args.weights_sha256,
            args.output_dir,
        )
