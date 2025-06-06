import os
import torch
import subprocess
import shutil
from huggingface_hub import hf_hub_download
from torchvision.models.video import mvit_v2_s, r3d_18


def download_model():
    # Retrieve the Hugging Face API key from environment (required at build time)
    token = os.getenv("HF_API_KEY")

    if not token:
        raise ValueError("HF_API_KEY environment variable is not set")
    
    repo_id = "heartwise/DeepCoro_CLIP_stenosis"
    file_name = "best_model_epoch_3.pt"
    output_dir = "models"
    
    # Ensure the models directory exists
    os.makedirs(output_dir, exist_ok=True)
    
    # Download the model file into the models directory
    model_path = hf_hub_download(repo_id=repo_id, filename=file_name, token=token, local_dir=output_dir)
    print(f"Downloaded model weights to {model_path}")
    
if __name__ == "__main__":
    download_model()