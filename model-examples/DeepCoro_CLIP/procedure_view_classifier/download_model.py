#!/usr/bin/env python3
"""Download and verify the immutable procedure view classifier artifacts.

Every artifact the runtime loads is pinned and checked:

* ``heartwise/VasoVision`` at an exact Hugging Face commit: the checkpoint (release
  provenance), plus ``config.json`` and ``class_mapping.json`` from the same commit.
* The ``facebookresearch/pytorchvideo`` hub repository at an exact Git commit, which
  ``models/vaso_vision.py`` loads with ``torch.hub.load(..., source="local")``.
* The Kinetics X3D-M checkpoint that ``x3d_m(pretrained=True)`` reads from the
  torch hub cache (every tensor is then replaced by the VasoVision state dict).
"""

from __future__ import annotations

import argparse
import hashlib
import shutil
import subprocess
import urllib.request
from pathlib import Path

EXPECTED_REPOSITORY = "heartwise/VasoVision"
EXPECTED_REVISION = "af6db088eae690c6eb64eabf4baf65ad7f7744b0"
EXPECTED_WEIGHTS_PATH = "vaso_vision.pt"
# Structural files read at runtime, from the same revision as the weights.
EXTRA_FILES_SHA256 = {
    "config.json": "9e7567eb51e5fc8669778f10a019f7c04ff42d36ec273e906e9fb14c61b782bd",
    "class_mapping.json": "840654f724b91ac1a6168e4b7a9da83053f2bbaabbde4fd0a62c06dd80cb4767",
}

PYTORCHVIDEO_REMOTE = "https://github.com/facebookresearch/pytorchvideo.git"
PYTORCHVIDEO_REVISION = "7da857b418b0e05fc4005907038695b247219b3a"
PYTORCHVIDEO_HUB_DIR = Path("facebookresearch/pytorchvideo")

X3D_M_URL = "https://dl.fbaipublicfiles.com/pytorchvideo/model_zoo/kinetics/X3D_M.pyth"
X3D_M_FILENAME = "X3D_M.pyth"
X3D_M_SHA256 = "eb9583d5f5a988a5c5f06f75760b070a5c9715794bd3b0536b78bd30595dcf67"
USER_AGENT = "PACS-AI-model-release/1.0"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as artifact:
        for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_file(path: Path, expected_sha256: str) -> Path:
    if not path.is_file():
        raise FileNotFoundError(f"Expected artifact was not downloaded: {path}")
    actual_sha256 = sha256_file(path)
    if actual_sha256 != expected_sha256:
        raise ValueError(
            f"SHA-256 mismatch for {path}: expected {expected_sha256}, received {actual_sha256}"
        )
    return path


def require_release_identity(repository: str, revision: str, weights_path: str) -> None:
    expected = (EXPECTED_REPOSITORY, EXPECTED_REVISION, EXPECTED_WEIGHTS_PATH)
    if (repository, revision, weights_path) != expected:
        raise ValueError(
            "Release metadata does not identify the validated VasoVision artifacts "
            f"({EXPECTED_REPOSITORY}@{EXPECTED_REVISION}:{EXPECTED_WEIGHTS_PATH})"
        )


def torch_hub_checkpoint() -> Path:
    import torch

    return Path(torch.hub.get_dir()) / "checkpoints" / X3D_M_FILENAME


def pytorchvideo_head(hub_dir: Path) -> str:
    # A detached checkout records the commit itself in HEAD; no git binary needed.
    head = (hub_dir / ".git" / "HEAD").read_text(encoding="utf-8").strip()
    if head != PYTORCHVIDEO_REVISION:
        raise ValueError(
            f"pytorchvideo hub repository is at {head!r}, expected {PYTORCHVIDEO_REVISION}"
        )
    if not (hub_dir / "hubconf.py").is_file():
        raise FileNotFoundError(f"pytorchvideo hubconf.py is missing in {hub_dir}")
    return head


def verify_artifacts(
    output_dir: Path,
    weights_path: str,
    expected_weights_sha256: str,
    hub_dir: Path = PYTORCHVIDEO_HUB_DIR,
) -> Path:
    weights = verify_file(output_dir / weights_path, expected_weights_sha256)
    for name, expected_sha256 in EXTRA_FILES_SHA256.items():
        verify_file(output_dir / name, expected_sha256)
    pytorchvideo_head(hub_dir)
    verify_file(torch_hub_checkpoint(), X3D_M_SHA256)
    return weights


def download_vasovision(
    token: str,
    repository: str,
    revision: str,
    weights_path: str,
    expected_weights_sha256: str,
    output_dir: Path,
) -> Path:
    from huggingface_hub import snapshot_download

    if not token:
        raise ValueError("A Hugging Face access token is required")
    output_dir.mkdir(parents=True, exist_ok=True)
    snapshot_download(
        repo_id=repository,
        revision=revision,
        token=token,
        local_dir=str(output_dir),
        repo_type="model",
        allow_patterns=[weights_path, *EXTRA_FILES_SHA256],
    )
    for name, expected_sha256 in EXTRA_FILES_SHA256.items():
        verify_file(output_dir / name, expected_sha256)
    weights = verify_file(output_dir / weights_path, expected_weights_sha256)
    print(f"Verified {repository}@{revision}:{weights_path} ({expected_weights_sha256})")
    return weights


def install_pytorchvideo_hub(hub_dir: Path = PYTORCHVIDEO_HUB_DIR) -> None:
    """Check out the pinned pytorchvideo commit for torch.hub source="local"."""

    if hub_dir.exists():
        shutil.rmtree(hub_dir)
    hub_dir.mkdir(parents=True)
    for command in (
        ("git", "init", "--quiet"),
        ("git", "remote", "add", "origin", PYTORCHVIDEO_REMOTE),
        ("git", "fetch", "--quiet", "--depth", "1", "origin", PYTORCHVIDEO_REVISION),
        ("git", "checkout", "--quiet", "--detach", "FETCH_HEAD"),
    ):
        subprocess.run(command, cwd=hub_dir, check=True)  # nosec B603 - fixed arguments
    resolved = subprocess.run(
        ("git", "rev-parse", "HEAD"), cwd=hub_dir, check=True, capture_output=True, text=True
    ).stdout.strip()
    if resolved != PYTORCHVIDEO_REVISION:
        raise ValueError(f"pytorchvideo checkout resolved to {resolved}")
    pytorchvideo_head(hub_dir)
    print(f"Checked out {PYTORCHVIDEO_REMOTE}@{PYTORCHVIDEO_REVISION}")


def download_x3d_checkpoint() -> Path:
    """Pre-seed the torch hub cache so x3d_m(pretrained=True) never fetches at runtime."""

    destination = torch_hub_checkpoint()
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix(destination.suffix + ".part")
    request = urllib.request.Request(X3D_M_URL, headers={"User-Agent": USER_AGENT})
    try:
        with urllib.request.urlopen(request) as response, temporary.open("wb") as output:  # nosec B310
            shutil.copyfileobj(response, output, length=1024 * 1024)
        verify_file(temporary, X3D_M_SHA256)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)
    print(f"Verified {X3D_M_URL} ({X3D_M_SHA256})")
    return destination


def warm_hub_model(hub_dir: Path = PYTORCHVIDEO_HUB_DIR) -> None:
    """Load x3d_m once exactly as the runtime does, from the pinned local artifacts."""

    import torch

    model = torch.hub.load(str(hub_dir), "x3d_m", pretrained=True, source="local")
    del model
    # The cached checkpoint must be the verified one, not a fresh download.
    verify_file(torch_hub_checkpoint(), X3D_M_SHA256)


def read_token(token_file: str | None) -> str:
    if not token_file:
        raise ValueError("Pass --token-file (the hf_token BuildKit secret)")
    return Path(token_file).read_text(encoding="utf-8").strip()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--weights-path", required=True)
    parser.add_argument("--weights-sha256", required=True)
    parser.add_argument("--output-dir", type=Path, default=Path("models"))
    parser.add_argument("--hub-dir", type=Path, default=PYTORCHVIDEO_HUB_DIR)
    parser.add_argument("--token-file")
    parser.add_argument("--verify-only", action="store_true")
    args = parser.parse_args()

    require_release_identity(args.repository, args.revision, args.weights_path)
    if args.verify_only:
        verify_artifacts(args.output_dir, args.weights_path, args.weights_sha256, args.hub_dir)
        print("Verified packaged VasoVision, pytorchvideo and X3D-M artifacts")
        return

    download_vasovision(
        read_token(args.token_file),
        args.repository,
        args.revision,
        args.weights_path,
        args.weights_sha256,
        args.output_dir,
    )
    install_pytorchvideo_hub(args.hub_dir)
    download_x3d_checkpoint()
    warm_hub_model(args.hub_dir)
    verify_artifacts(args.output_dir, args.weights_path, args.weights_sha256, args.hub_dir)


if __name__ == "__main__":
    main()
