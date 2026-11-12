from typing import Any

from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field


class Config(BaseModel):
    class ModelConfig(BaseModel):
        architectureFile: str
        weightsFile: str
        workers: int = Field(gt=0)
        batchSize: int = Field(gt=0)

    modelDirectory: str
    models: dict[str, ModelConfig]


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
    class ModelRecommendations(BaseModel):
        en: str | None = None
        fr: str | None = None
        presentable: bool | None = None

    predictions: Any
    diagnosis: str | None = None
    modelRecommendations: ModelRecommendations | None


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
