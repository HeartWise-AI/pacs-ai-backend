"""Download and verify the DINOv3-SigLIP2 encoder and its probe heads.

The release manifest (data/model_info.json provenance) names the encoder checkpoint
(``--weights-path`` base/epoch_030.pt, ``--weights-sha256``). The probe heads come from the same
immutable revision of heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030 (probes/**) and are checked
against the repository's probes/SHA256SUMS: every listed file must be present with its hash.
The repository mirrors /mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/{base,probes}.
"""
import argparse
import hashlib
from pathlib import Path

PROBES = "probes"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as checkpoint:
        for chunk in iter(lambda: checkpoint.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_weights(output_dir: Path, weights_path: str, expected_sha256: str) -> Path:
    weights = output_dir / weights_path
    if not weights.is_file():
        raise FileNotFoundError(f"Expected checkpoint was not downloaded: {weights}")
    actual_sha256 = sha256_file(weights)
    if actual_sha256 != expected_sha256:
        raise ValueError(
            f"Checkpoint SHA-256 mismatch for {weights}: "
            f"expected {expected_sha256}, received {actual_sha256}"
        )
    return weights


def verify_probes(output_dir: Path) -> list[Path]:
    """Every file of probes/SHA256SUMS, present and with its hash."""
    directory = output_dir / PROBES
    sums = directory / "SHA256SUMS"
    if not sums.is_file():
        raise FileNotFoundError(f"Expected {sums} was not downloaded")
    verified = []
    for line in sums.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        expected, name = line.split(maxsplit=1)
        name = name.lstrip("*").strip()
        verified.append(verify_weights(directory, name, expected.lower()))
    if not verified:
        raise ValueError(f"{sums} lists no files")
    return verified


def verify_all(output_dir: Path, weights_path: str, weights_sha256: str) -> list[Path]:
    return [verify_weights(output_dir, weights_path, weights_sha256)] + verify_probes(output_dir)


def download_model(
    token: str,
    repository: str,
    revision: str,
    weights_path: str,
    expected_sha256: str,
    output_dir: Path = Path("models"),
) -> list[Path]:
    from huggingface_hub import snapshot_download

    if not token:
        raise ValueError("A Hugging Face access token is required")
    output_dir.mkdir(parents=True, exist_ok=True)
    snapshot_download(
        repo_id=repository,
        revision=revision,
        token=token,
        local_dir=str(output_dir),
        allow_patterns=[weights_path, f"{PROBES}/*", f"{PROBES}/**"],
    )
    verified = verify_all(output_dir, weights_path, expected_sha256)
    print(f"Verified {repository}@{revision}: {weights_path} and {len(verified) - 1} probe files")
    return verified


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
        for path in verify_all(args.output_dir, args.weights_path, args.weights_sha256):
            print(f"Verified {path}")
    else:
        download_model(
            read_token(args.token_file),
            args.repository,
            args.revision,
            args.weights_path,
            args.weights_sha256,
            args.output_dir,
        )
