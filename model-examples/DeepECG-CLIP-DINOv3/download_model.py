"""Download and verify the released DeepECG-CLIP DINOv3 checkpoints.

The release manifest (data/model_info.json provenance) names one checkpoint, the ACO head
(``--weights-path``/``--weights-sha256``). The other fine-tuned heads are pinned in
models/config.json ("Checkpoints": path + SHA-256) and come from the same immutable model
revision. Frozen linear probes, when the revision has any under "ProbeDirectory", are
verified against that directory's SHA256SUMS, and then the frozen pretraining encoder they
read ("FrozenEncoder", pinned by SHA-256) is downloaded too. Every file is verified; a missing
file or a different byte fails the build.

Source of the files: Hugging Face ``heartwise/deepecg_clip_dinov3_acs`` (private), uploaded
from the NAS release folder /mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/
(its SHA256SUMS lists the same hashes).
"""
import argparse
import hashlib
import json
from pathlib import Path

CONFIG_NAME = "config.json"


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


def pinned_checkpoints(output_dir: Path, weights_path: str, weights_sha256: str) -> dict[str, str]:
    """{path: sha256} of every checkpoint the service loads. The manifest checkpoint must be
    one of the heads of models/config.json, with the same hash."""
    config = json.loads((output_dir / CONFIG_NAME).read_text(encoding="utf-8"))
    pinned = {entry["path"]: entry["sha256"] for entry in config["Checkpoints"].values()}
    if pinned.get(weights_path) != weights_sha256:
        raise ValueError(
            f"{weights_path} with SHA-256 {weights_sha256} is not a checkpoint of models/{CONFIG_NAME}"
        )
    return pinned


def _config(output_dir: Path) -> dict:
    return json.loads((output_dir / CONFIG_NAME).read_text(encoding="utf-8"))


def probe_files(output_dir: Path) -> list[Path]:
    """The verified probe files of the download (none when the revision has no probes)."""
    from models.probes import verify_probe_files

    return verify_probe_files(output_dir / _config(output_dir)["ProbeDirectory"])


def frozen_encoder(output_dir: Path) -> tuple[str, str]:
    entry = _config(output_dir)["FrozenEncoder"]
    return entry["path"], entry["sha256"]


def verify_all(output_dir: Path, weights_path: str, weights_sha256: str) -> list[Path]:
    verified = [
        verify_weights(output_dir, path, sha256)
        for path, sha256 in pinned_checkpoints(output_dir, weights_path, weights_sha256).items()
    ]
    probes = probe_files(output_dir)
    if probes:
        verified += probes + [verify_weights(output_dir, *frozen_encoder(output_dir))]
    return verified


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

    pinned = pinned_checkpoints(output_dir, weights_path, expected_sha256)
    probe_directory = _config(output_dir)["ProbeDirectory"]

    def fetch(patterns: list[str]) -> None:
        snapshot_download(
            repo_id=repository,
            revision=revision,
            token=token,
            local_dir=str(output_dir),
            allow_patterns=patterns,
        )

    fetch(sorted(pinned) + [f"{probe_directory}/*", f"{probe_directory}/**"])
    if probe_files(output_dir):
        fetch([frozen_encoder(output_dir)[0]])
    verified = verify_all(output_dir, weights_path, expected_sha256)
    for path in verified:
        print(f"Verified {repository}@{revision}:{path.relative_to(output_dir)}")
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
