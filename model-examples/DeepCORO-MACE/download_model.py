import argparse
import os

from huggingface_hub import snapshot_download


REPO_ID = "heartwise/deepcoro_clip_mace"
REVISION = "7be030d1c7b62e41f45a4dd35adff9dae21d884f"
CHECKPOINT = "11zt0zl5_20250723-162407/models/best_model_epoch_18.pt"


def download_model(token: str) -> None:
    if not token:
        raise ValueError("A Hugging Face access token is required")

    output_dir = "models"
    os.makedirs(output_dir, exist_ok=True)
    local_dir = snapshot_download(
        repo_id=REPO_ID,
        revision=REVISION,
        token=token,
        local_dir=output_dir,
        allow_patterns=[CHECKPOINT],
    )
    print(f"Downloaded pinned DeepCORO-MACE checkpoint to {local_dir}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    args = parser.parse_args()
    download_model(args.token)
