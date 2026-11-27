# main.py
from fastapi import FastAPI, Request
from fastapi.staticfiles import StaticFiles
import json
import os
from datetime import datetime
import signal
import asyncio
import time
import base64

from fastapi.concurrency import run_in_threadpool

from utils.http_utils import (
    Config,
    HTMLPredictionResponse, 
    OHIFPredictionResponse, 
    HTTPResponse, 
    PDFPredictionResponse, 
    PredictRequest, 
    JsonPredictionResponse, 
    WebAppPredictionResponse
)
from logic import CustomPredictionService
from utils.fast_request import parse_predict_request, read_body
from utils.resource_config import create_inference_semaphore, load_model_info, model_info_payload
from utils.model_lifecycle import (
    ModelLifecycle,
    install_model_lifecycle_routes,
    records_model_activity,
)

root_path = os.getcwd()

with open(os.path.join(root_path, 'config.json'), 'r') as f:
    config_dict = json.load(f)

config = Config(**config_dict)

model_info = load_model_info(os.path.join(root_path, "data", "model_info.json"))
served_model_info = model_info_payload(
    model_info,
    source_revision=os.getenv("PACS_AI_SOURCE_REVISION") or None,
    model_version=os.getenv("PACS_AI_MODEL_VERSION") or None,
)
inference_lock = create_inference_semaphore(model_info)

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
model_lifecycle = ModelLifecycle(PredictionService, config, inference_lock)
install_model_lifecycle_routes(app, model_lifecycle)

last_request_time = datetime.now()

@app.on_event("startup")
async def startup_event():
    asyncio.create_task(check_inactivity())

async def check_inactivity():
    global last_request_time
    while True:
        await asyncio.sleep(1)  # Async sleep
        current_time = datetime.now()
        time_difference = (current_time - last_request_time).total_seconds()
                
        if time_difference > 600:  # 10 minutes threshold
            if PredictionService.is_initialized: # Only reset server if models are loaded
                print("No requests received for 10 minutes. Restarting server...")
                PredictionService.stop_model()
                os.kill(os.getpid(), signal.SIGTERM)
            else:
                last_request_time = datetime.now()

@app.middleware("http")
async def update_last_request_time(request: Request, call_next):
    global last_request_time
    if records_model_activity(request.url.path):
        last_request_time = datetime.now()
    response = await call_next(request)
    return response

@app.post("/inference/predict")
async def predict(http_request: Request):
    # The body is read and parsed here rather than by FastAPI: see utils/fast_request.py.
    started = time.perf_counter()
    body = await read_body(http_request)
    received = time.perf_counter()
    body_bytes = len(body)
    request = parse_predict_request(body)
    del body
    print(
        f"EchoAlign request: {body_bytes / 2**20:.0f} MiB received in {received - started:.2f}s, "
        f"parsed in {time.perf_counter() - received:.2f}s"
    )
    async with model_lifecycle.inference():
        try:
            PredictionService.load_model(config)
        except Exception as e:
            return HTTPResponse(
                status=500,
                success=False,
                message=str(e),
                error_code="MODEL_ERROR"
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

RENDER_REPORT_MAX_BYTES = 4 * 1024 * 1024  # a stored result (all heads + per-clip rows) is well under 1 MB
RENDER_REPORT_DRAIN_BYTES = 64 * 1024 * 1024


class ResultError(ValueError):
    """The stored result cannot be rendered."""


def _reject_constant(name: str):
    raise ValueError(f"{name} is not valid JSON")


async def _bounded_body(request: Request, limit: int):
    """The request body, or None when it is larger than `limit` bytes (drained so nginx passes the 413 on)."""
    body, seen = bytearray(), 0
    async for chunk in request.stream():
        seen += len(chunk)
        if seen <= limit:
            body += chunk
        elif seen > RENDER_REPORT_DRAIN_BYTES:
            break
    return bytes(body) if seen <= limit else None


def _render_stored_result(payload) -> str:
    if not isinstance(payload, dict) or set(payload) != {"result"}:
        raise ResultError('body must be {"result": <the data object of a JSON-mode prediction>}')
    result = payload["result"]
    predictions = result.get("predictions") if isinstance(result, dict) else None
    if not isinstance(predictions, dict) or not isinstance(predictions.get("keyHeads"), dict) \
            or not isinstance(predictions.get("summary"), dict):
        raise ResultError("result.predictions with summary and keyHeads is required")
    return CustomPredictionService.render_html(predictions)


@app.post("/inference/render-report")
async def render_report(request: Request):
    """HTML report (with the research-use banner) of a stored JSON-mode result. CPU only: no model load, no
    forward pass, and it does not count as model activity."""
    body = await _bounded_body(request, RENDER_REPORT_MAX_BYTES)
    if body is None:
        return HTTPResponse(status=413, success=False, error_code="PAYLOAD_TOO_LARGE",
                            message=f"Body larger than {RENDER_REPORT_MAX_BYTES} bytes").to_response()
    try:
        payload = json.loads(body, parse_constant=_reject_constant)
    except (ValueError, RecursionError) as e:
        return HTTPResponse(status=422, success=False, error_code="INVALID_RESULT",
                            message=f"Body is not valid JSON: {type(e).__name__}").to_response()
    try:
        html = await run_in_threadpool(_render_stored_result, payload)
    except ResultError as e:
        return HTTPResponse(status=422, success=False, error_code="INVALID_RESULT",
                            message=f"Cannot render this result: {e}").to_response()
    except Exception as e:  # a shape the checks let through but the renderer cannot draw
        print(f"render-report failed: {e!r}")
        return HTTPResponse(status=422, success=False, error_code="INVALID_RESULT",
                            message=f"Cannot render this result: {type(e).__name__}").to_response()
    return HTTPResponse(
        status=200, success=True, message="Report rendered",
        data={"htmlBase64": base64.b64encode(html.encode("utf-8")).decode("ascii"),
              "modelVersion": served_model_info["version"]},
    ).to_response()



@app.get("/inference/model-info")
async def get_model_info():
    try:
        return HTTPResponse(
            status=200,
            success=True,
            message="Model info retrieved successfully",
            data=served_model_info,
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