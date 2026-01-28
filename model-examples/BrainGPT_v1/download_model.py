import os
import argparse
from huggingface_hub import snapshot_download


def download_model(token: str) -> None:
    """
    Download the BrainGPT model from HuggingFace.
    
    This script is called during Docker build to fetch model weights.
    The model is downloaded to the local `models/` directory.
    
    Args:
        token: HuggingFace API token with read access to the model repo
    """
     
    repo_id = "Charliebear/BrainGPT"
    
    # This path must match what pipeline_script.py expects
    output_dir = "models"
    
    os.makedirs(output_dir, exist_ok=True)

    print(f"Downloading model from {repo_id}...")
    
    local_dir = snapshot_download(
        repo_id=repo_id,
        token=token,
        local_dir=output_dir,
        local_dir_use_symlinks=False  # Use actual files, not symlinks
    )
    
    print(f"Successfully downloaded model to {local_dir}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Download BrainGPT model from HuggingFace"
    )
    parser.add_argument(
        "--token",
        type=str,
        required=False,
        default=None,
        help="HuggingFace API token"
    )
    args = parser.parse_args()
    
    download_model(args.token)