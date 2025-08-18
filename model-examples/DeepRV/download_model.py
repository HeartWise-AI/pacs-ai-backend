import os
import argparse

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


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    args = parser.parse_args()
    token = args.token
    
    download_model(token)
