from typing import Any

from pydantic import BaseModel, Field
from fastapi.responses import JSONResponse

class HeadStructure(BaseModel):
    contrast_agent: int
    main_structure: int
    stent_presence: int
    dominance: int

class Config(BaseModel):
    frames: int
    resize: int
    head_structure: HeadStructure
    mean: list[float]
    std: list[float]
    class_mapping: str
    model_path: str
    # Videos per GPU forward and CPU threads preparing them (not in the weights repository's config.json, so
    # these defaults are what the image runs). Measured on a 37-video study (MHI, 2026-11-15): 1 video per
    # forward with 8 threads takes 11 s against 35 s with 1 thread, with bit-identical predictions and 0.8 GiB
    # of GPU memory; 8 videos per forward takes the same 11 s but 7.6 GiB and moves the dominance probability
    # by up to 0.0015. So batching stays at 1. forward_batch_size is part of the preprocessing contract and of
    # the declared GPU memory: change the three together.
    forward_batch_size: int = Field(default=1, ge=1, le=16)
    preprocess_workers: int = Field(default=8, ge=1, le=16)


class HTTPResponse:
    def __init__(
        self,
        status: int,
        success: bool,
        message: str,
        data: Any | None = None,
        error_code: Any | None = None,
    ):
        self.status = status
        self.success = success
        self.message = message
        self.error_code = error_code
        self.data = data if data is not None else {}

    def _convert_to_dict(self, obj):
        if isinstance(obj, BaseModel):
            return obj.model_dump()  # Using model_dump() instead of dict()
        if isinstance(obj, dict):
            return {k: self._convert_to_dict(v) for k, v in obj.items()}
        if isinstance(obj, list):
            return [self._convert_to_dict(item) for item in obj]
        return obj

    def to_response(self) -> JSONResponse:
        response_data = {
            "success": self.success,
            "message": self.message,
            "data": self._convert_to_dict(self.data),
        }

        if self.error_code is not None:
            response_data["errorCode"] = self.error_code

        return JSONResponse(content=response_data, status_code=self.status)


class PredictRequest(BaseModel):
    seriesInstanceImages: dict[int, dict[int, str]] | None = None
    seriesInstanceMetadata: dict[str, Any] | None = None
    additionalMetadata: dict[str, Any] | None = None
    outputMode: str

class JsonPredictionResponse(BaseModel):
    predictions: list[dict[str, str | float]]  # labels, and dominance_left_probability since 1.0.3

class OHIFPredictionResponse(BaseModel):
    class Segmentation(BaseModel):
        labelmap: str
        dimensions: list[int]
        label: str
        segments: dict[str, int]

    segmentation: Segmentation
    measurements: list[Any] = Field(default_factory=list)


class HTMLPredictionResponse(BaseModel):
    htmlBase64: str = Field(
        ..., title="Base64 Encoded HTML", description="A base64 encoded HTML string."
    )


class WebAppPredictionResponse(BaseModel):
    webappPath: str = Field(
        ..., title="Web Application Path", description="The path to the web application viewer."
    )
    webappDataBase64: str = Field(
        ...,
        title="Base64 Encoded Webapp Data",
        description="Base64 encoded data for the web application.",
    )


class PDFPredictionResponse(BaseModel):
    pdfBase64: str = Field(
        ..., title="Base64 Encoded PDF", description="A base64 encoded string of the PDF."
    )
