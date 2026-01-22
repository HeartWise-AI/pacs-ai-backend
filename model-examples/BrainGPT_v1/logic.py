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

            print(f"Saved {count} images in {input_dir}")

            
            cmd = ["python", "pipeline_script.py", "--input_dir", input_dir, "--output_dir", output_dir]
            result = subprocess.run(cmd, capture_output=True, text=True, cwd="/app")

            
            try:
                with open(os.path.join(output_dir, "result.txt"), "r") as f:
                    report = f.read()
            except FileNotFoundError:
                report = f"Erreur script: {result.stderr}"

           
            response = {"report": report}
        
            if request.outputMode == "HTML": # Génération du rapport HTML propre 
                from datetime import datetime
                timestamp = datetime.now().strftime("%Y-%m-%d %H:%M")
               
             # HTML template moderne avec CSS intégré njj 
                html = f"""
                <!DOCTYPE html>
                <html lang="fr">
                <head>
                    <meta charset="UTF-8">
                    <meta name="viewport" content="width=device-width, initial-scale=1.0">
                    <title>Rapport BrainGPT</title>
                    <style>
                        :root {{
                            --primary-color: #2563eb;
                            --bg-color: #f3f4f6;
                            --card-bg: #ffffff;
                            --text-main: #1f2937;
                            --text-secondary: #6b7280;
                        }}
                        body {{
                            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                            background-color: var(--bg-color);
                            color: var(--text-main);
                            margin: 0;
                            padding: 20px;
                            line-height: 1.6;
                        }}
                        .container {{
                            max-width: 800px;
                            margin: 0 auto;
                            background: var(--card-bg);
                            padding: 40px;
                            border-radius: 12px;
                            box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
                        }}
                        header {{
                            border-bottom: 2px solid #e5e7eb;
                            padding-bottom: 20px;
                            margin-bottom: 30px;
                        }}
                        h1 {{
                            color: var(--primary-color);
                            margin: 0;
                            font-size: 1.8rem;
                            display: flex;
                            align-items: center;
                            gap: 10px;
                        }}
                        .meta {{
                            color: var(--text-secondary);
                            font-size: 0.9rem;
                            margin-top: 5px;
                        }}
                        .report-section {{
                            background-color: #eff6ff;
                            border-left: 4px solid var(--primary-color);
                            padding: 20px;
                            border-radius: 4px;
                            font-size: 1.05rem;
                            white-space: pre-wrap; /* Preserve les sauts de ligne du rapport */
                        }}
                        details {{
                            margin-top: 40px;
                            padding-top: 20px;
                            border-top: 1px solid #e5e7eb;
                        }}
                        summary {{
                            cursor: pointer;
                            color: var(--text-secondary);
                            font-weight: 600;
                            user-select: none;
                        }}
                        summary:hover {{
                            color: var(--primary-color);
                        }}
                        pre {{
                            background: #1e293b;
                            color: #e2e8f0;
                            padding: 15px;
                            border-radius: 8px;
                            overflow-x: auto;
                            font-size: 0.85rem;
                            font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
                        }}
                        .status-badge {{
                            display: inline-block;
                            padding: 2px 8px;
                            border-radius: 9999px;
                            font-size: 0.75rem;
                            font-weight: bold;
                            background-color: #dcfce7;
                            color: #166534;
                            margin-left: 10px;
                            vertical-align: middle;
                        }}
                    </style>
                </head>
                <body>
                    <div class="container">
                        <header>
                            <h1>🧠 Rapport BrainGPT <span class="status-badge">Complété</span></h1>
                            <div class="meta">ID Analyse: {run_id} | Date: {timestamp}</div>
                        </header>

                        <h3>Résultats de l'analyse</h3>
                        <div class="report-section">{report}</div>

                        <details>
                            <summary>Afficher les logs techniques (Debug)</summary>
                            <pre>{result.stdout}</pre>
                            {f"<br><strong>Erreurs (stderr):</strong><pre style='color:#fca5a5'>{result.stderr}</pre>" if result.stderr else ""}
                        </details>
                    </div>
                </body>
                </html>
                """
             
                response["htmlBase64"] = base64.b64encode(html.encode('utf-8')).decode()
               

            shutil.rmtree(f"/app/temp/{run_id}") # Nettoyage
            return True, response

        except Exception as e:
            return False, {"message": str(e)}