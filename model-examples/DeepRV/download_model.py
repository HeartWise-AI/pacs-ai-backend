import os
import shutil
import argparse
import subprocess

import torch
from huggingface_hub import hf_hub_download


def download_model(token):
    # Retrieve the Hugging Face API key from environment (required at build time)
    if not token:
        raise ValueError("HF_API_KEY environment variable is not set")

    repo_id = "heartwise/DeepRV_x3d"
    file_name = "deeprv_x3d.pt"
    output_dir = "models"

    # Ensure the models directory exists
    os.makedirs(output_dir, exist_ok=True)

    # Download the model file into the models directory
    model_path = hf_hub_download(
        repo_id=repo_id, filename=file_name, token=token, local_dir=output_dir
    )
    print(f"Downloaded model weights to {model_path}")


def download_pytorchvideo_hub():
    """Pre-download PyTorch Hub model architecture at build time for offline use."""
    print("Downloading x3d_m architecture from PyTorch Hub...")
    
    # Clone pytorchvideo repo to local path for source="local" usage
    local_repo_path = "facebookresearch/pytorchvideo"
    
    if os.path.exists(local_repo_path):
        print(f"Removing existing {local_repo_path}...")
        shutil.rmtree(local_repo_path)
    
    os.makedirs("facebookresearch", exist_ok=True)
    
    print(f"Cloning pytorchvideo repository to {local_repo_path}...")
    subprocess.run(
        ["git", "clone", "--depth", "1", "https://github.com/facebookresearch/pytorchvideo.git", local_repo_path],
        check=True
    )
    
    # Load model once to download and cache the pretrained weights
    model = torch.hub.load(
        local_repo_path, "x3d_m", pretrained=True, source="local"
    )
    del model  # Free memory after caching
    print("PyTorch Hub model cached successfully for offline use")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    args = parser.parse_args()
    token = args.token
    
    download_model(token)
    download_pytorchvideo_hub()
