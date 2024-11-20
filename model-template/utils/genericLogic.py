from utils.http_utils import HTTPResponse, PredictRequest
import numpy as np
import torch
import gc

class BasePredictionService:
    models = {}
    is_initialized = False
    
    async def predict(self, request: PredictRequest):
        output_mode = request.outputMode

        if output_mode not in ["JSON", "OHIF_ANNOTATIONS", "HTML", "WEB_APP", "PDF"]:
            return False, self._handle_unsupported_output()
        
        if not self.__class__.is_initialized:
            return False, self._handle_uninitialized_models()
        
        # Dictionary mapping output modes to their respective handler methods
        handlers = {
            "JSON": self._handle_json_output,
            "OHIF_ANNOTATIONS": self._handle_ohif_output,
            "HTML": self._handle_html_output,
            "WEB_APP": self._handle_webapp_output,
            "PDF": self._handle_pdf_output
        }
        
        handler = handlers.get(output_mode)
        if handler and self.__class__.is_initialized:
            return True, await handler(request)

    async def _handle_json_output(self, request: PredictRequest):
        return {
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

    async def _handle_ohif_output(self, request: PredictRequest):
        return {
                "metadata": {"key": "value pair"},
                "segmentations": [],
                "boundingBoxes": [],
                "measurements": []
            }

    async def _handle_html_output(self, request: PredictRequest):
        return {"htmlBase64": "base64 encoded html..."}

    async def _handle_webapp_output(self, request: PredictRequest):
        return {
                "webappPath": "/app/viewer",
                "webappDataBase64": "base64 encoded webapp data..."
            }

    async def _handle_pdf_output(self, request: PredictRequest):
        return {"pdfBase64": "base64 encoded pdf..."}
    
    def load_model(self, model_weights_path: str):
        """
        Abstract method that must be implemented by child classes
        """
        raise NotImplementedError("Method load_model must be implemented in the custom logic class")
    
    @classmethod
    def inference(cls, model_input, model_key: str):
        return cls.models[model_key](model_input)

    @classmethod
    def unload_model(cls):
        """Clear all loaded models from GPU and memory"""
        #TODO Improve this method, it doesn't fully clear the models from memory
        for _, model in cls.models.items():
            del model
        
        if torch.cuda.is_available():
            torch.cuda.empty_cache()
        
        gc.collect()
        cls.is_initialized = False

    def _handle_unsupported_output(self):
        return HTTPResponse(
            status=400,
            success=False,
            message="Unsupported output mode",
            error_code="UNSUPPORTED_OUTPUT_MODE"
        ).to_response()
    
    def _handle_uninitialized_models(self):
        return HTTPResponse(
            status=500,
            success=False,
            message="Models not initialized",
            error_code="MODELS_NOT_INITIALIZED"
        ).to_response()