import base64
import os
import shutil
import subprocess
import uuid
from typing import Tuple, Dict, Any
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest, JsonPredictionResponse

class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):

        self.is_initialized = True

    async def predict(self, request: PredictRequest) -> Tuple[bool, Dict[str, Any]]:
        # 1. Préparer les dossiers
        run_id = str(uuid.uuid4())
        input_dir = f"/app/temp/{run_id}/input"
        output_dir = f"/app/temp/{run_id}/output"
        os.makedirs(input_dir, exist_ok=True)
        os.makedirs(output_dir, exist_ok=True)

        try:
            # 2. Sauvegarder les DICOMs reçus
            if not request.seriesInstanceImages:
                return False, {"message": "No images received"}

            count = 0
            for series in request.seriesInstanceImages.values():
                for uid, b64 in series.items():
                    with open(os.path.join(input_dir, f"{uid}.dcm"), "wb") as f:
                        f.write(base64.b64decode(b64))
                    count += 1
            print(f"Sauvegardé {count} images dans {input_dir}")

            # 3. LANCER SCRIPT
            cmd = ["python", "pipeline_script.py", "--input_dir", input_dir, "--output_dir", output_dir]
            result = subprocess.run(cmd, capture_output=True, text=True, cwd="/app")

            # 4. Lire le résultat
            try:
                with open(os.path.join(output_dir, "result.txt"), "r") as f:
                    report = f.read()
            except FileNotFoundError:
                report = f"Erreur script: {result.stderr}"

            # 5. Réponse HTML 
            response = {"report": report}
            if request.outputMode == "HTML":
                html = f"<html><body><h3>Rapport BrainGPT</h3><p>{report}</p><br><pre>Logs: {result.stdout}</pre></body></html>"
                response["htmlBase64"] = base64.b64encode(html.encode()).decode()

            shutil.rmtree(f"/app/temp/{run_id}") # Nettoyage
            return True, response

        except Exception as e:
            return False, {"message": str(e)}