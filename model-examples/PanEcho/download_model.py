import argparse
import os

from huggingface_hub import hf_hub_download


DEFAULT_REPO_ID = "VoyagerWSH/20EchoViewClassifier"
DEFAULT_FILENAME = "best_model_pretrained_echoprime_updated.pth"


def download_model(token: str, output_dir: str) -> str:
    if not token:
        raise ValueError("HF token is required to download PanEcho view-classifier weights")

    os.makedirs(output_dir, exist_ok=True)

    return hf_hub_download(
        repo_id=DEFAULT_REPO_ID,
        filename=DEFAULT_FILENAME,
        token=token,
        local_dir=output_dir,
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    parser.add_argument("--output-dir", type=str, default="weights")
    args = parser.parse_args()

    local_path = download_model(token=args.token, output_dir=args.output_dir)
    print(f"Downloaded view-classifier weights to {local_path}")
