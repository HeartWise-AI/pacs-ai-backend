"""Download the pinned VasoVision run classifier (models/run_filter.json) and verify its checkpoint SHA-256."""
import argparse
import json
from pathlib import Path

from download_model import verify_weights


def download_run_filter(token: str, manifest: Path = Path("models/run_filter.json")) -> Path:
    from huggingface_hub import snapshot_download

    spec = json.loads(manifest.read_text(encoding="utf-8"))
    output_dir = Path(spec["directory"])
    output_dir.mkdir(parents=True, exist_ok=True)
    snapshot_download(repo_id=spec["repository"], revision=spec["revision"], token=token,
                      local_dir=str(output_dir), allow_patterns=spec["files"])
    for name in spec["files"]:
        if not (output_dir / name).is_file():
            raise FileNotFoundError(f"Expected run-filter file was not downloaded: {output_dir / name}")
    weights = verify_weights(output_dir, spec["weightsPath"], spec["weightsSha256"])
    print(f"Verified {spec['repository']}@{spec['revision']}:{spec['weightsPath']} ({spec['weightsSha256']})")
    return weights


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token-file", required=True)
    args = parser.parse_args()
    download_run_filter(Path(args.token_file).read_text(encoding="utf-8").strip())
