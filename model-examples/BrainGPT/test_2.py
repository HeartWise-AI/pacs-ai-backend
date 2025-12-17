import sys
import os

otter_parent_path = "/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT"

flamingo_parent_path = "/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/otter"



if otter_parent_path not in sys.path:
    sys.path.append(otter_parent_path)

if flamingo_parent_path not in sys.path:
    sys.path.append(flamingo_parent_path)


import torch
import numpy as np
from PIL import Image
from torchvision import transforms
import nibabel as nib
import numpy as np
from pathlib import Path
import matplotlib.pyplot as plt
import torch
from otter.modeling_otter import OtterForConditionalGeneration




    
model_path = "/home/tibia/Documents/LLM/BrainGPT/checkpoints/OTTER_CLIP_BRAINGPT_hf/" # REMPLACER CHEMIN PAR CELUI DANS LE DOCKER
device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
model = OtterForConditionalGeneration.from_pretrained(model_path)
model.to(device)
model.eval()

FLAMINGO_MEAN = [0.481, 0.458, 0.408]
FLAMINGO_STD = [0.269, 0.261, 0.276]
patch_image_size = 224 

transform_pipeline = transforms.Compose([
                transforms.Resize(
                    (patch_image_size, patch_image_size), 
                    interpolation=transforms.InterpolationMode.BICUBIC
                ),
                transforms.ToTensor(),
                transforms.Normalize(mean=FLAMINGO_MEAN, std=FLAMINGO_STD),
            ])  

     
INPUT_DIR = Path("/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/temp/ID_00526c11_ID_d6296de728.nii.gz") # PUT WHATEVER PATH YOU WANT HERE with NIfTI FILE


try:
    img = nib.load(str(INPUT_DIR))
    data = img.get_fdata() 
            
    total_slices = data.shape[2]
    if total_slices < 24:

        print(f"Warning: Seulement {total_slices} slices (attendu >= 24)")
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

    volume_tensor = torch.stack(processed_slices_tensors)
    print ("Volume tensor shape before unsqueeze:", volume_tensor.shape)
    volume_tensor = volume_tensor.unsqueeze(0).unsqueeze(1)
    print ("Volume tensor shape after unsqueeze:", volume_tensor.shape)
          
    vision_x = volume_tensor.to(device,dtype=model.dtype)
    tokenizer = model.text_tokenizer
    
  
        
        
    instruction = ( 
    "You are provided with brain CT slices from a single study. "
    "The number of slices is 24 . "
    "Please generate medical descriptions based on the images in a consistent style. "
    "Use the following guidelines: - Degree: Indicate the intensity or state (e.g., normal, mild, chronic, old, etc). "
    "- Landmark: Specify the area of interest (e.g., intracerebral, midline, parenchyma, sulci, etc). "
    "- Feature: Describe any observed abnormalities (e.g., hemorrhage, atrophy, infarcts, etc). "
    "- Impression: Conclude with a clinical impression (e.g., arteriosclerotic encephalopathy, intracerebral hemorrhage, dementia, etc). "
    "Ensure consistency and clarity in the report." )
            # "You are an AI assistant specialized in radiology topics. "
            # "Which organs do these CT slices belong to?") # ICI ON PEUT CHANGER L'INSTRUCTION SELON BESOIN
     
    prompt = f"<image>User: {instruction} GPT:<answer>"
        
       
    lang_x = tokenizer(
            [prompt],
            return_tensors="pt",
        )
        

    lang_x_input_ids = lang_x["input_ids"].to(device)
    lang_x_attention_mask = lang_x["attention_mask"].to(device)


    with torch.no_grad():
        generated_ids = model.generate(
                        vision_x=vision_x,
                        lang_x=lang_x_input_ids,
                        attention_mask=lang_x_attention_mask,
                        max_new_tokens = 512
                    ) 

     
    output_text = tokenizer.decode(generated_ids[0])
        
    if "<answer>" in output_text:
            output_text = output_text.split("<answer>")[-1]
        
    output_text = output_text.replace("<|endofchunk|>", "").strip().strip('"')
    
    print("\n--- Résultat de l'inférence BrainGPT ---")   
    print("Generated Text:", output_text)
    
except Exception as e:
    print(f"Erreur lors du traitement NIfTI: {e}")
    raise e
    
    
    
    
    
    