# main.py
from fastapi import FastAPI, Request
from fastapi.staticfiles import StaticFiles
from fastapi.responses import JSONResponse, RedirectResponse
from pydantic import BaseModel
from typing import Any, Optional, Union, List
from pathlib import Path
import json
import os
from dotenv import load_dotenv

# Load environment variables
load_dotenv()

root_path = os.getenv('ROOT_PATH')

app = FastAPI(
    title="PACS.AI Inference Model API",
    description="API Documentation of PACS.AI Model Inference",
    version="1.0.0",
    docs_url=None,  # Disable default docs
    redoc_url=None  # Disable default redoc
)

# Mount static files for documentation
app.mount("/docs", StaticFiles(directory=os.path.join(root_path, "docs"), html=True), name="docs")

class PredictRequest(BaseModel):
    inferences: Any
    age: int
    gender: str
    outputMode: str

class HTTPResponse:
    def __init__(
        self, 
        status: int, 
        success: bool, 
        message: str, 
        data: Optional[Any] = None, 
        error_code: Optional[str] = None
    ):
        self.status = status
        self.success = success
        self.message = message
        self.error_code = error_code
        self.data = data if data is not None else {}

    def to_response(self) -> JSONResponse:
        response_data = {
            "success": self.success,
            "message": self.message,
        }
        
        if self.error_code is not None:
            response_data["errorCode"] = self.error_code
        if self.data:
            response_data["data"] = self.data

        return JSONResponse(
            content=response_data,
            status_code=self.status
        )

@app.post("/inference/predict")
async def predict(request: PredictRequest):
    output_mode = request.outputMode

    if output_mode == "JSON":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data={
                "diagnosis": "limit",
                "predictions": {
                    "Vessel": {
                        "probability": 56.534433434343,
                        "confidence": "intermediate",
                        "presentable": True,
                        "displayResult": "Left Coronary"
                    },
                    "LVEF": {
                        "probability": 65.34343433232,
                        "confidence": "low",
                        "presentable": True,
                        "displayResult": 42.2
                    }
                },
                "modelRecommendations": {
                    "en": "Recommendation for the next model",
                    "fr": "Recommandation pour le prochain modèle",
                    "presentable": True
                }
            }
        ).to_response()

    elif output_mode == "OHIF_ANNOTATIONS":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data={
                "metadata": {"key": "value pair"},
                "segmentations": [],
                "boundingBoxes": [],
                "measurements": []
            }
        ).to_response()

    elif output_mode == "HTML":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data={"htmlBase64": "base64 encoded html..."}
        ).to_response()

    elif output_mode == "WEB_APP":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data={
                "webappPath": "/app/viewer",
                "webappDataBase64": "base64 encoded webapp data..."
            }
        ).to_response()

    elif output_mode == "PDF":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data={"pdfBase64": "base64 encoded pdf..."}
        ).to_response()

    else:
        return HTTPResponse(
            status=400,
            success=False,
            message="Unsupported output mode",
            error_code="UNSUPPORTED_OUTPUT_MODE"
        ).to_response()

@app.get("/inference/model-info")
async def get_model_info():
    try:
        data_path = os.path.join(root_path, "data")
        
        with open(os.path.join(data_path, "model_info.json"), "r") as f:
            model_info = json.load(f)
            
        return HTTPResponse(
            status=200,
            success=True,
            message="Model info retrieved successfully",
            data=model_info
        ).to_response()
    
    except Exception as e:
        return HTTPResponse(
            status=500,
            success=False,
            message="Failed to read model info",
            error_code="MODEL_ERROR"
        ).to_response()

@app.get("/inference/model-facts")
async def get_model_facts():
    try:
        data_path = os.path.join(root_path, "data")
        
        with open(os.path.join(data_path, "model_facts.json"), "r") as f:
            model_facts = json.load(f)
            
        return HTTPResponse(
            status=200,
            success=True,
            message="Model facts retrieved successfully",
            data=model_facts
        ).to_response()
    
    except Exception as e:
        return HTTPResponse(
            status=500,
            success=False,
            message="Failed to read model facts",
            error_code="MODEL_ERROR"
        ).to_response()

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)