# main.py
import asyncio
import json
import os
import signal
from datetime import datetime, timezone

from fastapi import FastAPI, Request
from fastapi.staticfiles import StaticFiles
from logic import CustomPredictionService
from utils.http_utils import (
    Config,
    HTMLPredictionResponse,
    HTTPResponse,
    JsonPredictionResponse,
    OHIFPredictionResponse,
    PDFPredictionResponse,
    PredictRequest,
    WebAppPredictionResponse,
)

root_path = os.getcwd()

with open(os.path.join(root_path, "config.json")) as f:
    config_dict = json.load(f)

config = Config(**config_dict)

app = FastAPI(
    title="PACS.AI Inference Model API",
    description="API Documentation of PACS.AI Model Inference",
    version="1.0.0",
    docs_url=None,  # Disable default docs
    redoc_url=None,  # Disable default redoc
)

PredictionService = CustomPredictionService()

# Mount static files for documentation
app.mount("/docs", StaticFiles(directory=os.path.join(root_path, "docs"), html=True), name="docs")

last_request_time = datetime.now(tz=timezone.utc)


@app.on_event("startup")
async def startup_event():
    asyncio.create_task(check_inactivity())


async def check_inactivity():
    global last_request_time
    while True:
        await asyncio.sleep(1)  # Async sleep
        current_time = datetime.now(tz=timezone.utc)
        time_difference = (current_time - last_request_time).total_seconds()

        if time_difference > 600:  # 10 minutes threshold
            if PredictionService.is_initialized:  # Only reset server if models are loaded
                print("No requests received for 10 minutes. Restarting server...")
                PredictionService.stop_model()
                os.kill(os.getpid(), signal.SIGTERM)
            else:
                last_request_time = datetime.now(tz=timezone.utc)


@app.middleware("http")
async def update_last_request_time(request: Request, call_next):
    global last_request_time
    last_request_time = datetime.now(tz=timezone.utc)
    return await call_next(request)


@app.post("/inference/predict")
async def predict(request: PredictRequest):
    try:
        PredictionService.load_model(config)
    except Exception as e:
        return HTTPResponse(
            status=500, success=False, message=str(e), error_code="MODEL_ERROR"
        ).to_response()

    success, response = await PredictionService.predict(request)
    if not success:
        return response

    output_mode = request.outputMode
    if output_mode == "JSON":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=JsonPredictionResponse(**response),
        ).to_response()

    if output_mode == "OHIF_ANNOTATIONS":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=OHIFPredictionResponse(**response),
        ).to_response()

    if output_mode == "HTML":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=HTMLPredictionResponse(**response),
        ).to_response()

    if output_mode == "WEB_APP":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=WebAppPredictionResponse(**response),
        ).to_response()

    if output_mode == "PDF":
        return HTTPResponse(
            status=200,
            success=True,
            message="Prediction successful",
            data=PDFPredictionResponse(**response),
        ).to_response()

    return HTTPResponse(
        status=400,
        success=False,
        message="Unsupported output mode",
        error_code="UNSUPPORTED_OUTPUT_MODE",
    ).to_response()


@app.get("/inference/model-info")
async def get_model_info():
    try:
        data_path = os.path.join(root_path, "data")

        with open(os.path.join(data_path, "model_info.json")) as f:
            model_info = json.load(f)

        return HTTPResponse(
            status=200, success=True, message="Model info retrieved successfully", data=model_info
        ).to_response()

    except Exception:
        return HTTPResponse(
            status=500,
            success=False,
            message="Failed to read model info",
            error_code="MODEL_ERROR",
        ).to_response()


@app.get("/inference/model-facts")
async def get_model_facts():
    try:
        data_path = os.path.join(root_path, "data")

        with open(os.path.join(data_path, "model_facts.json")) as f:
            model_facts = json.load(f)

        return HTTPResponse(
            status=200,
            success=True,
            message="Model facts retrieved successfully",
            data=model_facts,
        ).to_response()

    except Exception:
        return HTTPResponse(
            status=500,
            success=False,
            message="Failed to read model facts",
            error_code="MODEL_ERROR",
        ).to_response()


if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host="0.0.0.0", port=8000, reload=True)
