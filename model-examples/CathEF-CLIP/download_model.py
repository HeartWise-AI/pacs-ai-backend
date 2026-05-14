import os
import argparse
from huggingface_hub import snapshot_download


def download_model(token):
    # Retrieve the Hugging Face API key from environment (required at build time)
    token = token

    if not token:
        raise ValueError("HF_API_KEY environment variable is not set")

    repo_id = "heartwise/CathEF_CLIP"
    output_dir = "models"
    
    # Ensure the models directory exists
    os.makedirs(output_dir, exist_ok=True)

    # Download all files from the repository
    local_dir = snapshot_download(
        repo_id=repo_id,
        token=token,
        local_dir=output_dir,
        local_dir_use_symlinks=False  # Use actual files instead of symlinks
    )
    print(f"Downloaded all files from repository to {local_dir}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    args = parser.parse_args()
    token = args.token
    
    download_model(token)
