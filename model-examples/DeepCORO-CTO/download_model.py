import argparse
import os

from huggingface_hub import snapshot_download

REPO_ID = "heartwise/DeepCORO_CTO"
# Weights are pinned to a release tag so image builds are reproducible:
#   v1.0 — overall 5-head model, epoch 24 (W&B pdnzpnpy), up to 8 videos
#   v2.0 — per-artery 15-head model, epoch 18 (W&B dg1o32md), up to 10 videos
DEFAULT_REVISION = os.environ.get("DEEPCORO_CTO_HF_REVISION", "v2.0")
OUTPUT_DIR = "models"
# alt_models/ holds alternative overall checkpoints for research comparisons;
# they are not used by the service and would add ~330 MB to the image.
IGNORE_PATTERNS = ["alt_models/*", "alt_models/**"]


def download_model(token: str, revision: str = DEFAULT_REVISION) -> None:
    if not token:
        raise ValueError("A HuggingFace token is required (--token)")

    os.makedirs(OUTPUT_DIR, exist_ok=True)
    local_dir = snapshot_download(
        repo_id=REPO_ID,
        revision=revision,
        token=token,
        local_dir=OUTPUT_DIR,
        ignore_patterns=IGNORE_PATTERNS,
    )
    print(f"Downloaded {REPO_ID}@{revision} to {local_dir}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    parser.add_argument("--revision", type=str, default=DEFAULT_REVISION)
    args = parser.parse_args()

    download_model(args.token, args.revision)
