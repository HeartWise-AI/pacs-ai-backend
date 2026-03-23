import sys
import os
import torch
import numpy as np
from PIL import Image
from torchvision import transforms
import pydicom

# --- IMPORTS BRAINGPT ---
otter_parent_path = "/app/otter"
flamingo_parent_path = "/app/otter/flamingo"

if otter_parent_path not in sys.path:
    sys.path.append(otter_parent_path)
if flamingo_parent_path not in sys.path:
    sys.path.append(flamingo_parent_path)

from otter.modeling_otter import OtterForConditionalGeneration

class BrainGPTInference:
    def __init__(self):
        self.model = None
        self.device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
        self.tokenizer = None
        self.transform_pipeline = None
        
        
    def load_model(self):
        """Charge le modèle une seule fois en mémoire."""
        if self.model is not None:
            return # Déjà chargé

        print("--- LOADING BRAINGPT MODEL (31GB) ---")
        script_dir = os.path.dirname(os.path.abspath(__file__))
        model_path = os.path.join(script_dir, "models", "OTTER_CLIP_BRAINGPT_hf")
        
        self.model = OtterForConditionalGeneration.from_pretrained(
            model_path, local_files_only=True
        )
        self.model.to(self.device)
        self.model.eval()
        
        self.tokenizer = self.model.text_tokenizer
        
        # Pipeline de transformation d'image
        FLAMINGO_MEAN = [0.481, 0.458, 0.408]
        FLAMINGO_STD = [0.269, 0.261, 0.276]
        self.transform_pipeline = transforms.Compose([
            transforms.Resize((224, 224), interpolation=transforms.InterpolationMode.BICUBIC),
            transforms.ToTensor(),
            transforms.Normalize(mean=FLAMINGO_MEAN, std=FLAMINGO_STD),
        ])
        print("--- BRAINGPT READY ---")

    def predict(self, dicom_list: list):
        """Réalise une inférence sur les images DICOM dans le dossier input_dir."""
        if not dicom_list:
            return "Erreur: Liste DICOM vide."
        
        dicom_list.sort(key=lambda x: float(x.InstanceNumber) if hasattr(x, 'InstanceNumber') else 0)
        
        try:
            volume = np.stack([s.pixel_array.astype(float) for s in dicom_list])
        except Exception as e:
            return f"Erreur empilement volume: {str(e)}"
       
        data = np.transpose(volume, (1, 2, 0))

        # 2. Sélection 24 slices
        total_slices = data.shape[2]
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
            tensor_slice = self.transform_pipeline(img_pil)
            processed_slices_tensors.append(tensor_slice)

        # 3. Inférence
        volume_tensor = torch.stack(processed_slices_tensors)
        volume_tensor = volume_tensor.unsqueeze(0).unsqueeze(1)
        
        vision_x = volume_tensor.to(self.device, dtype=self.model.dtype)
        
        #instruction = "You are provided with brain CT slices from a single study. The number of slices is 24. Please generate medical descriptions based on the images in a consistent style."
        instruction = (" You are provided with brain CT slices from a single study. "
                        "The number of slices is 24. "
                        "Please generate medical descriptions based on the images in a consistent style. "
                        "Use the following guidelines: - Degree: Indicate the intensity or state (e.g., normal, mild, chronic, old, etc). "
                        "- Landmark: Specify the area of interest (e.g., intracerebral, midline, parenchyma, sulci, etc). "
                        "- Feature: Describe any observed abnormalities (e.g., hemorrhage, atrophy, infarcts, etc). "
                        "- Impression: Conclude with a clinical impression (e.g., arteriosclerotic encephalopathy, intracerebral hemorrhage, dementia, etc). "
                        "Ensure consistency and clarity in the report.")
                            
        
        prompt = f"<image>User: {instruction} GPT:<answer>"
       
        
        lang_x = self.tokenizer([prompt], return_tensors="pt")
        lang_x_input_ids = lang_x["input_ids"].to(self.device)
        lang_x_attention_mask = lang_x["attention_mask"].to(self.device)

        with torch.no_grad():
            generated_ids = self.model.generate(
                vision_x=vision_x,
                lang_x=lang_x_input_ids,
                attention_mask=lang_x_attention_mask,
                max_new_tokens=512
            )

        output_text = self.tokenizer.decode(generated_ids[0])
        if "<answer>" in output_text:
            output_text = output_text.split("<answer>")[-1]
        output_text = output_text.replace("<|endofchunk|>", "").strip().strip('"')
        
        return output_text