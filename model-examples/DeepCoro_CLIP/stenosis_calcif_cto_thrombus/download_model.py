import os
import argparse
from huggingface_hub import hf_hub_download


def download_model(token):
    # Retrieve the Hugging Face API key from environment (required at build time)
    token = token

    if not token:
        raise ValueError("HF_API_KEY environment variable is not set")

    repo_id = "heartwise/DeepCoro_CLIP_stenosis"
    file_name = "best_model_epoch_3.pt"
    output_dir = "models"
    subfolder = "cnw09vn8_01062025-140448"
    
    # Ensure the models directory exists
    os.makedirs(output_dir, exist_ok=True)

    # Download the model file into the models directory
    model_path = hf_hub_download(
        repo_id=repo_id,
        filename=file_name,
        subfolder=subfolder,
        token=token,
        local_dir=output_dir
    )
    print(f"Downloaded model weights to {model_path}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    args = parser.parse_args()
    token = args.token
    
    download_model(token)
