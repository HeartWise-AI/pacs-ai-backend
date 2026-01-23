import base64
import asyncio
from io import BytesIO
from datetime import datetime

import pydicom

from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest
from pipeline_script import BrainGPTInference


class CustomPredictionService(BasePredictionService):
    _brain_gpt: BrainGPTInference = None  # Attribut de CLASSE

    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:  # Vérifie l'attribut de CLASSE
            print("Modèle BrainGPT déjà initialisé.")
            return

        print("Initialize BrainGPT...")
        CustomPredictionService._brain_gpt = BrainGPTInference()
        CustomPredictionService._brain_gpt.load_model()
        CustomPredictionService.is_initialized = True 
        print("BrainGPT initialized.")

    def _process_dicoms(self, request: PredictRequest):
        dicoms = []
        if not request.seriesInstanceImages:
            return dicoms
        for series in request.seriesInstanceImages.values():
            for uid, b64 in series.items():
                dicom_bytes = base64.b64decode(b64)
                ds = pydicom.dcmread(BytesIO(dicom_bytes))
                dicoms.append(ds)
        print(f"Received {len(dicoms)} slices in memory.")
        return dicoms

    async def _run_inference(self, dicoms):
        loop = asyncio.get_event_loop()
        return await loop.run_in_executor(None, CustomPredictionService._brain_gpt.predict, dicoms)

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = self._process_dicoms(request)
        if not dicoms:
            return {"htmlBase64": base64.b64encode(b"<h1>No images received</h1>").decode()}

        report = await self._run_inference(dicoms)
        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M")
        html = f"""<!DOCTYPE html>
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
                        <div class="meta">Date: {timestamp}</div>
                        <div class="content">{report}</div>
                    </div>
                </body>
                </html>"""
        return {"htmlBase64": base64.b64encode(html.encode('utf-8')).decode()}

    async def _handle_json_output(self, request: PredictRequest):
        dicoms = self._process_dicoms(request)
        if not dicoms:
            return {
                "diagnosis": "No images received",
                "predictions": {},
                "modelRecommendations": {"en": "No images received", "fr": "Aucune image reçue", "presentable": True}
            }

        report = await self._run_inference(dicoms)
        return {
            "diagnosis": report,
            "predictions": {"text": report},
            "modelRecommendations": {"en": report, "fr": report, "presentable": True}
        }