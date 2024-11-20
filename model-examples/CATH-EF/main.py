# main.py
from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles
import json
import os
from dotenv import load_dotenv

from utils.http_utils import Config, HTMLPredictionResponse, OHIFPredictionResponse, HTTPResponse, PDFPredictionResponse, PredictRequest, JsonPredictionResponse, WebAppPredictionResponse
from logic import CustomPredictionService

# Load environment variables
load_dotenv()

root_path = os.getenv('ROOT_PATH')

with open(os.path.join(root_path, 'config.json'), 'r') as f:
    config_dict = json.load(f)

config = Config(**config_dict)

app = FastAPI(
    title="PACS.AI Inference Model API",
    description="API Documentation of PACS.AI Model Inference",
    version="1.0.0",
    docs_url=None,  # Disable default docs
    redoc_url=None  # Disable default redoc
)

PredictionService = CustomPredictionService()

# Mount static files for documentation
app.mount("/docs", StaticFiles(directory=os.path.join(root_path, "docs"), html=True), name="docs")

# @app.on_event("startup")
# async def on_startup():
#     PredictionService.load_model(config)

@app.get("/management/loadModels/")
async def load_model():
    try:
        PredictionService.load_model(config)
        return HTTPResponse(
            status=200,
            success=True,
            message="Model loaded successfully"
        ).to_response()
    
    except Exception as e:
        return HTTPResponse(
            status=500,
            success=False,
            message=str(e),
            error_code="MODEL_ERROR"
        ).to_response()

@app.get("/management/unloadModels/")
async def unload_model():
    try:
        PredictionService.unload_model()
        return HTTPResponse(
            status=200,
            success=True,
            message="Model unloaded successfully"
        ).to_response()
    
    except Exception as e:
        return HTTPResponse(
            status=500,
            success=False,
            message=str(e),
            error_code="MODEL_ERROR"
        ).to_response()

@app.post("/inference/predict")
async def predict(request: PredictRequest):
    succes, response = await PredictionService.predict(request)
    if not succes:
        return response
    
    output_mode = request.outputMode
    if output_mode == "JSON":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=JsonPredictionResponse(**response)
        ).to_response()

    elif output_mode == "OHIF_ANNOTATIONS":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=OHIFPredictionResponse(**response)
        ).to_response()

    elif output_mode == "HTML":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=HTMLPredictionResponse(**response)
        ).to_response()

    elif output_mode == "WEB_APP":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=WebAppPredictionResponse(**response)
        ).to_response()

    elif output_mode == "PDF":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=PDFPredictionResponse(**response)
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
    uvicorn.run("main:app", host="0.0.0.0", port=8000, reload=True)