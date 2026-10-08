import argparse
import hashlib
from pathlib import Path


REPOSITORY = "heartwise/deepcoro_clip_cardiosyntax"
REVISION = "1ffb38cfc10fa10c4b60f44746063feb860eeba0"
WEIGHTS_PATH = "v6_20260929-203527/models/best_model_epoch_19.pt"
WEIGHTS_SHA256 = "856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as checkpoint:
        for chunk in iter(lambda: checkpoint.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_weights(output_dir: Path) -> Path:
    weights = output_dir / WEIGHTS_PATH
    if not weights.is_file():
        raise FileNotFoundError(f"Expected checkpoint was not downloaded: {weights}")

    actual_sha256 = sha256_file(weights)
    if actual_sha256 != WEIGHTS_SHA256:
        raise ValueError(
            f"Checkpoint SHA-256 mismatch for {weights}: "
            f"expected {WEIGHTS_SHA256}, received {actual_sha256}"
        )
    return weights


def download_model(token: str, output_dir: Path = Path("models")) -> Path:
    from huggingface_hub import snapshot_download

    if not token:
        raise ValueError("A Hugging Face access token is required")

    output_dir.mkdir(parents=True, exist_ok=True)
    snapshot_download(
        repo_id=REPOSITORY,
        revision=REVISION,
        token=token,
        local_dir=str(output_dir),
        allow_patterns=[WEIGHTS_PATH],
    )
    weights = verify_weights(output_dir)
    print(f"Verified {REPOSITORY}@{REVISION}:{WEIGHTS_PATH} ({WEIGHTS_SHA256})")
    return weights


def read_token(token: str | None, token_file: str | None) -> str:
    if token:
        return token.strip()
    if token_file:
        return Path(token_file).read_text(encoding="utf-8").strip()
    raise ValueError("Pass --token or --token-file")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    token_source = parser.add_mutually_exclusive_group(required=True)
    token_source.add_argument("--token")
    token_source.add_argument("--token-file")
    args = parser.parse_args()
    download_model(read_token(args.token, args.token_file))
