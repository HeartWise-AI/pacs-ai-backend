import base64
import os
import shutil
from io import BytesIO
import uuid
import pydicom
import asyncio
from datetime import datetime
from typing import Tuple, Dict, Any
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest, JsonPredictionResponse
from pipeline_script import BrainGPTInference

class CustomPredictionService(BasePredictionService):
    def __init__(self):
        self.brain_gpt = BrainGPTInference() # On instancie la classe
        self.is_initialized = False
        
        
    def load_model(self, config: Config):
        if not self.is_initialized:
            print("Initialisation du modèle BrainGPT...")
            self.brain_gpt.load_model()  # Charge le modèle une seule fois
            self.is_initialized = True
            print("Modèle BrainGPT initialisé.")
        

    async def predict(self, request: PredictRequest) -> Tuple[bool, Dict[str, Any]]:
        if not self.is_initialized:
            return False, {"message": "Modèle non initialisé. Veuillez appeler load_model d'abord."}
        run_id = str(uuid.uuid4())
        # 1. Préparer les dossiers
        dicoms = []

        try:
            # 2. Sauvegarder les DICOMs reçus
            if not request.seriesInstanceImages:
                return False, {"message": "No images received"}

       
            for series in request.seriesInstanceImages.values():
                for uid, b64 in series.items():
                    dicom_bytes = base64.b64decode(b64)
                    ds = pydicom.dcmread(BytesIO(dicom_bytes))
                    dicoms.append(ds)
                    

            print(f"Received {len(dicoms)} slices in memory.")

        except Exception as e:
            return False, {"message": f"Error decoding DICOMs: {str(e)}"}
        
        loop = asyncio.get_event_loop()
        
        try: 
           report = await loop.run_in_executor(None, self.brain_gpt.predict, dicoms)
           
           
        except Exception as e:
            return False, {"message": f"Inference failed: {str(e)}"}
        
        
        response = {
            "report": report,
            "predictions": {"text": report}
        }
        
        # Génération du HTML
        if request.outputMode == "HTML":
            timestamp = datetime.now().strftime("%Y-%m-%d %H:%M")
            html = f"""
            <!DOCTYPE html>
            <html lang="fr">
            <head>
                <meta charset="UTF-8">
                <title>Rapport BrainGPT</title>
                <style>
                    body {{ font-family: sans-serif; padding: 20px; background: #f3f4f6; }}
                    .container {{ background: white; padding: 30px; border-radius: 10px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); }}
                    h1 {{ color: #2563eb; }}
                    .meta {{ color: #6b7280; font-size: 0.9em; margin-bottom: 20px; }}
                    .content {{ white-space: pre-wrap; background: #eff6ff; padding: 20px; border-radius: 5px; }}
                </style>
            </head>
            <body>
                <div class="container">
                    <h1>🧠 Rapport BrainGPT</h1>
                    <div class="meta">ID Analyse: {run_id} | Date: {timestamp}</div>
                    <div class="content">{report}</div>
                </div>
            </body>
            </html>
            """
            response["htmlBase64"] = base64.b64encode(html.encode('utf-8')).decode()

        return True, response
        