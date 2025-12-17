import torch
import numpy as np
import pydicom
from PIL import Image
from otter.modeling_otter import OtterForConditionalGeneration
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest, JsonPredictionResponse
from torchvision import transforms
import sys
import os
from transformers import CLIPImageProcessor

otter_parent_path = "/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT"
flamingo_parent_path = "/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/otter"

if otter_parent_path not in sys.path:
    sys.path.append(otter_parent_path)
 

if flamingo_parent_path not in sys.path:
    sys.path.append(flamingo_parent_path)
    
    
    
import os
import nibabel as nib
import numpy as np
from pathlib import Path
import matplotlib.pyplot as plt
import subprocess
import torch
from otter.modeling_otter import OtterForConditionalGeneration
from torch.utils.data import DataLoader
from mimicit_utils.mimicit_dataset import MimicitDataset


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        print("Chargement de BrainGPT...")
        model_path = "./models/OTTER_CLIP_BRAINGPT_hf" # Chemin DANS le Docker
        device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
        model = OtterForConditionalGeneration.from_pretrained(model_path)
        model.to(device)
        model.eval()

        image_processor = CLIPImageProcessor()
        self.models = {
            "model": model,
            "tokenizer": model.text_tokenizer,
            "transform": transform_pipeline,
            "device": device
        }
      
        FLAMINGO_MEAN = [0.481, 0.458, 0.408]
        FLAMINGO_STD = [0.269, 0.261, 0.276]
        patch_image_size = 224 # Taille d'image attendue par le modèle Otter/CLIP

        transform_pipeline = transforms.Compose([
                transforms.Resize(
                    (patch_image_size, patch_image_size), 
                    interpolation=transforms.InterpolationMode.BICUBIC
                ),
                transforms.ToTensor(), # Convertit [0, 255] -> [0.0, 1.0]
                transforms.Normalize(mean=FLAMINGO_MEAN, std=FLAMINGO_STD),
            ])

    async def _handle_json_output(self, request: PredictRequest):
        
        # A. On récupère les images
        if not request.seriesInstanceImages:
            raise ValueError("no images provided in the request")
            
        first_series = next(iter(request.seriesInstanceImages.values()))
        file_path = list(first_series.values())[0] # on prend le premier fichier DICOM ? (pas sur du tout ici)
        
        #  Nifti -> Tensor (24 slices)
        vision_x = self._process_nifti_to_tensor(file_path)

  
        
        generated_text = self._run_inference(vision_x)

        return JsonPredictionResponse(
            predictions={"report": generated_text},
            modelRecommendations={"en": generated_text, "presentable": True}
        )

    # --- fonction inference ---
    def _run_inference(self, vision_tensor):
       
        model = self.models["model"]
        tokenizer = self.models["tokenizer"]
        device = self.models["device"]

        # 1. Le Prompt (Instruction)
        prompt = "<image>User: Describe the CT slices. GPT:<answer>"
        
        # 2. Tokenization du texte
        lang_x = tokenizer(
            [prompt],
            return_tensors="pt",
        )
        
        # 3. Envoi sur GPU
        vision_x = vision_tensor.to(device).unsqueeze(0) # Ajout dimension batch si besoin
        lang_x_input_ids = lang_x["input_ids"].to(device)
        lang_x_attention_mask = lang_x["attention_mask"].to(device)

        # 4. GÉNÉRATION (Le cœur de BrainGPT)
        generated_ids = model.generate(
            vision_x=vision_x,
            lang_x=lang_x_input_ids,
            attention_mask=lang_x_attention_mask,
            max_new_tokens=512,
            num_beams=3,
            no_repeat_ngram_size=3
        )

        # 5. Décodage du résultat
        output_text = tokenizer.decode(generated_ids[0])
        
        # Nettoyage (optionnel, selon ce que renvoie le modèle)
        clean_text = output_text.split("<answer>")[-1].replace("<|endofchunk|>", "")
        
        return clean_text

    def _process_nifti_to_tensor(self, nii_path):
        """

        Lit le NIfTI, extrait 24 slices, normalise, et crée le tenseur pour Otter.
        """
        try:
            img = nib.load(str(nii_path))
            data = img.get_fdata() # (H, W, D)
            
            total_slices = data.shape[2]
            if total_slices < 24:

                print(f"Warning: Seulement {total_slices} slices (attendu >= 24)")
                indices = np.linspace(0, total_slices - 1, 24).astype(int)
            else:
                indices = np.linspace(0, total_slices - 1, 24).astype(int)

         

            processed_slices_tensors = []
            transform = self.models["transform"] # Le transform MimicIT

            for idx in indices:
                slice_2d = data[:, :, idx]
                
                # B. Normalisation 0-255 
                if np.ptp(slice_2d) == 0:
                    slice_norm = slice_2d
                else:
                    slice_norm = 255 * (slice_2d - np.min(slice_2d)) / (np.ptp(slice_2d) + 1e-8)
                
                slice_uint8 = slice_norm.astype(np.uint8)
                
                # C. Conversion PIL RGB (Requis avant le transform Torchvision)
                img_pil = Image.fromarray(slice_uint8).convert("RGB")
                
                # D. Application du Transform MimicIT (Resize -> ToTensor -> Normalize)
                tensor_slice = transform(img_pil) # Output shape: (3, 224, 224)
                
                processed_slices_tensors.append(tensor_slice)

            # E. Stack pour créer le volume
            # Liste de (3, 224, 224) -> Tensor (24, 3, 224, 224)
            volume_tensor = torch.stack(processed_slices_tensors)
            
            # F. Ajout des dimensions pour le modèle
            # BrainGPT (Otter) attend : (Batch_Size, Num_Images, Channels, H, W)
            # Donc : (1, 24, 3, 224, 224)
            volume_tensor = volume_tensor.unsqueeze(0)
            
            return volume_tensor
                
        
        except Exception as e:
            print(f"Erreur lors du traitement NIfTI: {e}")
            raise e
   
        
    def run_inference(self, vision_tensor):
        
        model = self.models["model"]
        tokenizer = self.models["tokenizer"]
        device = self.models["device"]
        
        
        instruction = (
            "You are an AI assistant specialized in radiology topics. "
            "Which organs do these CT slices belong to?"
        )
        # 1. Le Prompt (Instruction)
        prompt = f"<image>User: {instruction} GPT:<answer>"
        
        # 2. Tokenization du texte
        lang_x = tokenizer(
            [prompt],
            return_tensors="pt",
        )
        
        # 3. Envoi sur GPU
        vision_x = vision_tensor.to(device).unsqueeze(0) # Ajout dimension batch si besoin
        lang_x_input_ids = lang_x["input_ids"].to(device)
        lang_x_attention_mask = lang_x["attention_mask"].to(device)



        generated_ids = model.generate(
                    vision_x=vision_x,
                    lang_x=lang_x_input_ids,
                    attention_mask=lang_x_attention_mask,
                    max_new_tokens = 512
                ) 

        # 5. Décodage du résultat
        output_text = tokenizer.decode(generated_ids[0])
        
        if "<answer>" in output_text:
            output_text = output_text.split("<answer>")[-1]
        
        output_text = output_text.replace("<|endofchunk|>", "").strip().strip('"')
        
        return output_text