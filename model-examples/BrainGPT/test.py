# CT -> 24 slices PNG équidistantes -> Encodage base64 dans un JSON -> écriture fichier innstruction pour BrainGPT
# -> Preparation des données pour BrainGPT -> 

import sys
import os


# 1. Chemin pour l'importation 'otter' (Répertoire BrainGPT) et 'flamingo' (sous-répertoire otter)
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


INPUT_DIR = Path("/home/tibia/Documents/Brain_GPT_Dataset/black") #Chemin du Ct scans d'entrée
SLICE_OUTPUT_DIR = INPUT_DIR / "slices_png"
SLICE_OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

nii_files = list(INPUT_DIR.glob("*.nii.gz"))

for file in nii_files:
    img = nib.load(str(file))
    data = img.get_fdata()  # shape (H, W, D) où D > 24
    base_name = file.stem  # sans extension

    # Le nombre de tranches total dans le volume
    total_slices = data.shape[2]
    
    # Vérification: s'il y a moins de 24 tranches, on ne peut pas en sélectionner 24.
    if total_slices < 24:
        print(f"  {file.name} ignoré (seulement {total_slices} tranches)")
        continue


    selected_indices = np.linspace(0, total_slices - 1, 24).astype(int)

    # Sauvegarde les slices sélectionnées
    for i, slice_index in enumerate(selected_indices):
        slice_2d = data[:, :, slice_index]
        
        # Normalisation simple [0, 255]
        slice_norm = 255 * (slice_2d - np.min(slice_2d)) / (np.ptp(slice_2d) + 1e-8)
        slice_uint8 = slice_norm.astype(np.uint8)


        out_path = SLICE_OUTPUT_DIR / f"{base_name}_slice_sample_{i:02d}.png"
        plt.imsave(out_path, slice_uint8, cmap="gray")

    print(f"  {file.name} : 24 tranches (équidistantes) sauvegardées dans {SLICE_OUTPUT_DIR}")
    
    



# Liste tous les fichiers png dans le dossier merged
png_files = sorted(SLICE_OUTPUT_DIR.glob("*.png"))

for f in png_files:
    old_name = f.name  # ex: ID_441cea72_ID_4b13324a92.nii_slice00.png vient du RSNA

    parts = old_name.split("_")
    if len(parts) < 2:
        continue

    # On garde seulement les chiffres pour la première partie après ID pour que loadimage marche
    first_id = ''.join(filter(str.isdigit, parts[1]))  # '441cea72' -> '44172'

    # On fait meme pour la deuxieme partie
    second_id = ''.join(filter(str.isdigit, parts[3].split(".")[0]))  # '4b13324a92' -> '4132492'

    # Slice et extension
    slice_and_ext = "_".join(parts[4:])  # ex: slice00.png

    # Nouveau nom
    new_name = f"ID_{first_id}_ID_{second_id}_{slice_and_ext}"
    new_path = f.parent / new_name

    print(f"Renaming: {old_name} -> {new_name}")
    f.rename(new_path)
    
    
    


# Chemins
script_dir = "/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/MIIT/mimic-it/convert-it"
image_path = "/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/temp/slices_png"


command = [
    "python", 
    "main.py", 
    "--name=change.SpotTheDifference", 
    f"--image_path={image_path}", 
    "--num_threads=4"
]

print("Lancement du script...")

# Exécution
try:
    result = subprocess.run(
        command, 
        cwd=script_dir,  
        check=True,      
        capture_output=True, 
        text=True       
    )
    print("Succès !")
    print(result.stdout) # Affiche la sortie du script
    
except subprocess.CalledProcessError as e:
    print("Erreur lors de l'exécution :")
    print(e.stderr) # Affiche l'erreur
    
    
# Code pour faire le .json d'instruction 

import os
import json
import glob
from collections import defaultdict


# Chemin vers le dossier contenant les images brutes
dataset_path = "/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/temp/slices_png"

# Nom court du dataset (doit être le même que dans main.py)
short_name = "PACS_AI"

# L'instruction à donner au modèle
INSTRUCTION_TEXT = (
    "You are an AI assistant specialized in radiology topics. "
    " Which organs do these CT slices belong to? "
    
    # "You are provided with brain CT slices from a single study. "
    # "The number of slices is {num_slices}. "
    # "Please generate medical descriptions based on the images in a consistent style. "
    # "Use the following guidelines: - Degree: Indicate the intensity or state (e.g., normal, mild, chronic, old, etc). "
    # "- Landmark: Specify the area of interest (e.g., intracerebral, midline, parenchyma, sulci, etc). "
    # "- Feature: Describe any observed abnormalities (e.g., hemorrhage, atrophy, infarcts, etc). "
    # "- Impression: Conclude with a clinical impression (e.g., arteriosclerotic encephalopathy, intracerebral hemorrhage, dementia, etc). "
    # "Ensure consistency and clarity in the report."
)

# =================================================

def generate_instruction_json(dataset_path=dataset_path):
    # 1. Lister les images
    extensions = ["*.png", "*.jpg", "*.jpeg", "*.bmp"]
    all_files = []
    for ext in extensions:
        all_files.extend(glob.glob(os.path.join(dataset_path, ext)))
    
    if not all_files:
        all_files = [os.path.join(dataset_path, f) for f in os.listdir(dataset_path) 
                     if f.lower().endswith(('.png', '.jpg', '.jpeg', '.bmp'))]

    # 2. Grouper les images par "Étude" (Patient ID)
    studies = defaultdict(list)

    print(f"Traitement de {len(all_files)} fichiers...")

    for file_path in all_files:
        filename = os.path.basename(file_path)
        
        # --- Etape A : Générer l'ID unique de l'image (pour le lien vers le Registry) ---
        name_without_ext = os.path.splitext(filename)[0]
        clean_name = name_without_ext.replace(".nii", "").replace(".", "_")
        full_image_id = f"{short_name}_IMG_{clean_name}"
        
        # --- Etape B : Identifier l'étude (Patient) ---
        # Ex: ID_1423bd41_ID_72db71a879.nii_slice_sample_01.png
        # On coupe avant "_slice_sample_" pour avoir le nom du patient
        if "_slice_sample_" in filename:
            study_id_raw = filename.split("_slice_sample_")[0]
            
            # Récupération du numéro de slice pour le tri
            try:
                slice_num_str = filename.split("_slice_sample_")[-1].split(".")[0]
                slice_num = int(slice_num_str)
            except:
                slice_num = 0
            
            studies[study_id_raw].append((slice_num, full_image_id))
        else:
            print(f"Ignoré (format incorrect) : {filename}")

    # 3. Construire le JSON final
    json_output = {
        "meta": {
            "version": "0.0.2",
            "time": "2024-02",
            "author": "big_data_center"
        },
        "data": {}
    }

    print(f"Génération des instructions pour {len(studies)} études...")

    for study_id_raw, slices in studies.items():
        # IMPORTANT : Trier les slices dans l'ordre (0, 1, 2...)
        slices.sort(key=lambda x: x[0])
        sorted_image_ids = [item[1] for item in slices]
        
        # MODIFICATION ICI : Création de la clé basée sur le nom réel
        # On nettoie un peu le nom (enlève .nii et remplace les points par _)
        # Ex: ID_1423bd41.nii -> ID_1423bd41
        clean_study_key = study_id_raw.replace(".nii", "").replace(".", "_")
        
        # On peut ajouter un préfixe pour faire propre, ou laisser tel quel.
        # Ici je mets "INS_" + le nom réel pour indiquer que c'est une instruction.
        ins_key = f"INS_{clean_study_key}" 
        
        # Mise à jour du texte avec le vrai nombre de slices
        current_instruction = INSTRUCTION_TEXT.format(num_slices=len(sorted_image_ids))

        json_output["data"][ins_key] = {
            "instruction": current_instruction,
            "answer": "",
            "image_ids": sorted_image_ids,
            "rel_ins_ids": []
        }

    # 4. Sauvegarde
    output_filename = "instruction_dataset.json"
    with open(output_filename, "w") as f:
        json.dump(json_output, f, indent=4)

    print(f"Terminé ! Fichier '{output_filename}' généré.")

generate_instruction_json()



model = OtterForConditionalGeneration.from_pretrained(
                "/home/tibia/Documents/LLM/BrainGPT/checkpoints/OTTER_CLIP_BRAINGPT_hf/"
)

# Affichez le modèle pour vérifier qu'il est chargé (facultatif)
print(f"Modèle chargé : {type(model)}")

tokenizer = model.text_tokenizer


# 1. Création d'une fausse classe d'arguments pour remplacer argparse braingpt
class SimpleArgs:
    def __init__(self, tokenizer):
        # Paramètres requis par MimicitDataset.__init__
        self.tokenizer = tokenizer
        self.task = "inference"
        self.max_src_length = 512      # Valeur par défaut standard
        self.max_tgt_length = 512      # Valeur par défaut standard
        self.seed = 42                 # Valeur par défaut standard
        self.patch_image_size = 224  # Taille standard des patchs image
        self.inst_format= "simple"
        
        # Paramètres pour le DataLoader
        self.workers = 1
        
        


def get_inference_dataloader(args, tokenizer):
    
    
    """
    Version simplifiée pour l'inférence avec --mimicit_path et --images_path uniquement.
    """
    # 1. Configuration minimale requise par MimicitDataset
    args = SimpleArgs(tokenizer)
    
    # 2. Préparation des listes de chemins
    # MimicitDataset attend des listes, même pour un seul fichier
    mimicit_paths = ["/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/temp/instruction_dataset.json"] # CHEMIN DU JSON D'INSTRUCTION
    image_paths = ["/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/MIIT/mimic-it/convert-it/output/PACS_AI.json"] # CHEMIN DU JSON D'IMAGES PRODUIT PAR MIIT

    # Gestion de la config d'entraînement (vide mais necessaire poru compatibilité code)
    #  on passe une liste avec une chaîne vide
    train_config_paths = [""]

    

    status_list = ["new"]

    print(f"Chargement du dataset depuis : {mimicit_paths[0]} et {image_paths[0]}")

    # 3. Création de l'instance du Dataset
    #Classe du git branch mimicit 
    dataset = MimicitDataset(
        args, 
        mimicit_paths, 
        image_paths, 
        train_config_paths, 
        status_list=status_list
    )

    # 4. Création du DataLoader simple
    # - batch_size=1 : Pour traiter une image à la fois
    # - shuffle=False : IMPORTANT pour l'inférence (garder l'ordre des fichiers)

    dataloader = DataLoader(
        dataset,
        batch_size=1, 
        num_workers=args.workers,
        pin_memory=True,
        shuffle=False, 
        drop_last=False,
        collate_fn=dataset.collate 
    )

    # On retourne directement le dataloader (pas une liste de dataloaders)
    return dataloader


device_id = torch.device("cuda" if torch.cuda.is_available() else "cpu")
model.to(device_id)
batch_mimicit = next(iter(get_inference_dataloader(SimpleArgs(tokenizer), tokenizer)))


media_token_id = tokenizer("<image>", add_special_tokens=False)["input_ids"][-1]
endofchunk_token_id = tokenizer("<|endofchunk|>", add_special_tokens=False)["input_ids"][-1]
answer_token_id = tokenizer("<answer>", add_special_tokens=False)["input_ids"][-1]
ens_token_id = tokenizer(tokenizer.eos_token, add_special_tokens=False)["input_ids"][-1]

model.eval()


images = batch_mimicit["net_input"]["patch_images"].to(device_id, non_blocking=True)
input_ids = batch_mimicit["net_input"]["input_ids"].to(device_id, non_blocking=True)
attention_mask = batch_mimicit["net_input"]["attention_masks"].to(device_id, non_blocking=True)

labels = input_ids.clone()
labels[labels == tokenizer.pad_token_id] = -100
labels[:, 0] = -100
for i in range(labels.shape[0]):
                # remove loss for any token before the first <image> token
                # label_idx = 0
                # while label_idx < labels.shape[1] and labels[i][label_idx] != media_token_id:
                #     labels[i][label_idx] = -100
                #     label_idx += 1

                # <image>User: {cur_incontext_instruction} GPT:<answer> {cur_incontext_answer}<|endofchunk|>User: {instruction} GPT:<answer> {answer}<|endofchunk|>
                # <image>User: {cur_incontext_instruction} GPT:<answer> {cur_incontext_answer}<|endofchunk|><image>User: {instruction} GPT:<answer> {answer}<|endofchunk|>

                # get index of all endofchunk/media tokens in the sequence
    endofchunk_idxs = torch.where(labels[i] == endofchunk_token_id)[0]
    media_idxs = torch.where(labels[i] == media_token_id)[0]

                # remove loss for any token the before the first <answer>
    token_idx = 0
    while token_idx < labels.shape[1] and labels[i][token_idx] != answer_token_id:
        labels[i][token_idx] = -100
        token_idx += 1

                # remove loss for any token between <|endofchunk|> and <answer>, except <image>
        for endofchunk_idx in endofchunk_idxs[:-1]:
            token_idx = endofchunk_idx + 1
            while token_idx < labels.shape[1] and labels[i][token_idx] != answer_token_id:
                if labels[i][token_idx] == media_token_id:
                    pass
                else:
                    labels[i][token_idx] = -100
                    token_idx += 1

labels[labels == answer_token_id] = -100
labels[labels == media_token_id] = -100


device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
model.to(device)
dtype = model.dtype

test_prompt = "Describe the image in a single sentence."
lang_x = model.text_tokenizer(
                    [f"<image>User: {test_prompt} GPT:<answer>"],
                    return_tensors="pt",
                )
print(f" lang x : {lang_x}")

lang_x_input_ids = lang_x["input_ids"].to(device)
lang_x_attention_mask = lang_x["attention_mask"].to(device)



generated_text = model.generate(
                    vision_x=images.to(dtype),
                    lang_x=lang_x_input_ids,
                    attention_mask=lang_x_attention_mask,
                    max_new_tokens = 512,
                ) 

# géneration du texte 
import pandas as pd

generated_captions = {}


parsed_output = (
                    model.text_tokenizer.decode(generated_text[0])
                    .split("<answer>")[-1]
                    .lstrip()
                    .rstrip()
                    .split("<|endofchunk|>")[0]
                    .lstrip()
                    .rstrip()
                    .lstrip('"')
                    .rstrip('"')
                )
gt = (
                    model.text_tokenizer.decode(input_ids[0])
                    .split("<answer>")[-1]
                    .lstrip()
                    .rstrip()
                    .split("<|endofchunk|>")[0]
                    .lstrip()
                    .rstrip()
                    .lstrip('"')
                    .rstrip('"')
                )
                # print(batch_mimicit.keys())
#                 print("/",parsed_output,"/")
generated_captions[batch_mimicit["id"][0]] = (gt, parsed_output)
                # print(generated_captions.keys())

    # print(generated_captions)
df_data = [(key, val[0], val[1]) for key, val in generated_captions.items()]
df = pd.DataFrame(df_data, columns=['id', 'gt', 'parsed_output'])
print(df)

df.to_csv("/home/tibia/Documents/pacs-ai/pacs-ai-backend/model-examples/BrainGPT/temp/output3", index=False)