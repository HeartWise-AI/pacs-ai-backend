import os
import argparse
import time
from huggingface_hub import hf_hub_download


def download_model(token):
    # Retrieve the Hugging Face API key from environment (required at build time)
    token = token

    if not token:
        raise ValueError("HF_API_KEY environment variable is not set")

    repo_id = "VoyagerWSH/20EchoViewClassifier"
    output_dir = "weights"
    
    # Ensure the models directory exists
    os.makedirs(output_dir, exist_ok=True)

    local_dir = hf_hub_download(
        repo_id= repo_id,
        filename="best_model_pretrained_echoprime_updated.pth", 
        token=token,
        local_dir=output_dir 
    )

    print(f"Downloaded all files from repository to {local_dir}")

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--token", type=str, required=True)
    args = parser.parse_args()
    token = args.token
    
    download_model(token)
