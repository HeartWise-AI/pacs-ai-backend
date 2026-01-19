import sys
import os
import argparse  # Ajouté pour recevoir les arguments de logic.py
import torch
import numpy as np
from PIL import Image
from torchvision import transforms
import pydicom     
from pathlib import Path

# --- IMPORTS BRAINGPT ---

otter_parent_path = "/app/otter"
flamingo_parent_path = "/app/otter/flamingo"

if otter_parent_path not in sys.path:
    sys.path.append(otter_parent_path)
if flamingo_parent_path not in sys.path:
    sys.path.append(flamingo_parent_path)

from otter.modeling_otter import OtterForConditionalGeneration

def main():
    # 1. Récupération des dossiers d'entrée/sortie (envoyés par logic.py)
    parser = argparse.ArgumentParser()
    parser.add_argument("--input_dir", type=str, required=True, help="Dossier contenant les DICOMs")
    parser.add_argument("--output_dir", type=str, required=True, help="Dossier où écrire le résultat")
    args = parser.parse_args()

    print(f"--- WORKER: Démarrage sur {args.input_dir} ---")

    # 2. Chargement du modèle
    # Chemin interne au Docker ou il faut copier les poids
    model_path = "/app/models/OTTER_CLIP_BRAINGPT_hf"
    
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    print(f"Loading model from {model_path} on {device}...")
    
    try:
        model = OtterForConditionalGeneration.from_pretrained(
            model_path, 
            local_files_only=True
        )
        model.to(device)
        model.eval()
    except Exception as e:
        print(f"ERREUR CHARGEMENT MODELE: {e}")
        sys.exit(1)

    # 3. Pipeline de transformation 
    FLAMINGO_MEAN = [0.481, 0.458, 0.408]
    FLAMINGO_STD = [0.269, 0.261, 0.276]
    patch_image_size = 224 

    transform_pipeline = transforms.Compose([
        transforms.Resize((patch_image_size, patch_image_size), interpolation=transforms.InterpolationMode.BICUBIC),
        transforms.ToTensor(),
        transforms.Normalize(mean=FLAMINGO_MEAN, std=FLAMINGO_STD),
    ])  

    # 4. Lecture des Données : 

    try:
        dicom_files = [os.path.join(args.input_dir, f) for f in os.listdir(args.input_dir)]
        slices = []
        for f in dicom_files:
            try:
                ds = pydicom.dcmread(f)
                slices.append(ds)
            except:
                continue 
        

        slices.sort(key=lambda x: float(x.InstanceNumber) if hasattr(x, 'InstanceNumber') else 0)
        
        if not slices:
            raise ValueError("Aucun fichier DICOM valide trouvé dans le dossier.")

        # Création du volume numpy (Depth, H, W) -> Transpose vers (H, W, Depth)
        volume = np.stack([s.pixel_array.astype(float) for s in slices])
        data = np.transpose(volume, (1, 2, 0)) 

        print(f"Volume chargé: {data.shape}")

        # 5. Sélection des 24 slices 
        total_slices = data.shape[2]
        if total_slices < 24:
            indices = np.linspace(0, total_slices - 1, 24).astype(int)
        else:
            indices = np.linspace(0, total_slices - 1, 24).astype(int)

        processed_slices_tensors = []

        for idx in indices:
            slice_2d = data[:, :, idx]
            
            if np.ptp(slice_2d) == 0:
                slice_norm = slice_2d
            else:
                slice_norm = 255 * (slice_2d - np.min(slice_2d)) / (np.ptp(slice_2d) + 1e-8)
            
            slice_uint8 = slice_norm.astype(np.uint8)
            img_pil = Image.fromarray(slice_uint8).convert("RGB")
            tensor_slice = transform_pipeline(img_pil) 
            processed_slices_tensors.append(tensor_slice)

        # 6. Préparation Tenseurs 
        volume_tensor = torch.stack(processed_slices_tensors)
        # (24, 3, 224, 224) -> (1, 1, 24, 3, 224, 224)
        volume_tensor = volume_tensor.unsqueeze(0).unsqueeze(1) 
        
        vision_x = volume_tensor.to(device, dtype=model.dtype)
        tokenizer = model.text_tokenizer
        
        # 7. Inférence 
        instruction = ( 
            "You are provided with brain CT slices from a single study. "
            "The number of slices is 24. "
            "Please generate medical descriptions based on the images in a consistent style."
        )
        prompt = f"<image>User: {instruction} GPT:<answer>" # à modifier
        
        lang_x = tokenizer([prompt], return_tensors="pt")
        lang_x_input_ids = lang_x["input_ids"].to(device)
        lang_x_attention_mask = lang_x["attention_mask"].to(device)

        with torch.no_grad():
            generated_ids = model.generate(
                vision_x=vision_x,
                lang_x=lang_x_input_ids,
                attention_mask=lang_x_attention_mask,
                max_new_tokens=512
            ) 

        output_text = tokenizer.decode(generated_ids[0])
        if "<answer>" in output_text:
            output_text = output_text.split("<answer>")[-1]
        output_text = output_text.replace("<|endofchunk|>", "").strip().strip('"')

        print("Résultat généré.")

        # 8 Résultat
        with open(os.path.join(args.output_dir, "result.txt"), "w") as f:
            f.write(output_text)

    except Exception as e:
        print(f"CRASH WORKER: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()