from fastapi.responses import JSONResponse
from typing import Any, Dict, List, Optional, TypeAlias

from pydantic import BaseModel, Field

# Type alias for nested dictionary structure
SeriesInstanceMetadata: TypeAlias = Dict[str, Any]
SeriesInstanceMetadata: TypeAlias = Dict[int, Dict[int, str]]
SeriesInstanceViewMetadata: TypeAlias = Dict[int, Dict[int, Dict[str, Any]]]

class Config(BaseModel):
    class ModelConfig(BaseModel):
        model_path: str
    
    modelDirectory: str
    models: Dict[str, ModelConfig]

class HTTPResponse:
    def __init__(
        self, 
        status: int, 
        success: bool, 
        message: str, 
        data: Optional[Any] = None, 
        error_code: Optional[Any] = None
    ):
        self.status = status
        self.success = success
        self.message = message
        self.error_code = error_code
        self.data = data if data is not None else {}

    def _convert_to_dict(self, obj):
        if isinstance(obj, BaseModel):
            return obj.model_dump()  # Using model_dump() instead of dict()
        elif isinstance(obj, dict):
            return {k: self._convert_to_dict(v) for k, v in obj.items()}
        elif isinstance(obj, list):
            return [self._convert_to_dict(item) for item in obj]
        return obj

    def to_response(self) -> JSONResponse:
        response_data = {
            "success": self.success,
            "message": self.message,
            "data": self._convert_to_dict(self.data)
        }
        
        if self.error_code is not None:
            response_data["errorCode"] = self.error_code

        return JSONResponse(
            content=response_data,
            status_code=self.status
        )

class PredictRequest(BaseModel):
    outputMode: str
    additionalMetadata: Optional[Dict[str, Any]] = None
    seriesInstanceMetadata: Optional[SeriesInstanceMetadata] = None
    seriesInstanceImages: Optional[
        SeriesInstanceMetadata | SeriesInstanceViewMetadata
    ] = None

class JsonPredictionResponse(BaseModel):
    class ModelRecommendations(BaseModel):
        en: Optional[str] = None
        fr: Optional[str] = None
        presentable: Optional[bool] = None

    predictions: Any
    diagnosis: Optional[str] = None
    modelRecommendations: Optional[ModelRecommendations]

class OHIFPredictionResponse(BaseModel):
    class Segmentation(BaseModel):
        labelmap: str
        dimensions: List[int]
        label: str
        segments: Dict[str, int]

    segmentation: Segmentation
    measurements: List[Any] = Field(default_factory=list)

class HTMLPredictionResponse(BaseModel):
    htmlBase64: str = Field(..., title="Base64 Encoded HTML", description="A base64 encoded HTML string.")

class WebAppPredictionResponse(BaseModel):
    webappPath: str = Field(..., title="Web Application Path", description="The path to the web application viewer.")
    webappDataBase64: str = Field(..., title="Base64 Encoded Webapp Data", description="Base64 encoded data for the web application.")

class PDFPredictionResponse(BaseModel):
    pdfBase64: str = Field(..., title="Base64 Encoded PDF", description="A base64 encoded string of the PDF.")
