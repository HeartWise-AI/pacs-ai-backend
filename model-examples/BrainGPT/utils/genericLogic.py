import json
import os
from typing import Any

from utils.http_utils import HTTPResponse, PredictRequest


class BasePredictionService:
    models: dict[str, Any] = {}
    is_initialized: bool = False
    model_info: dict[str, Any] = {}
    supported_output_modes: list[str] = []

    @classmethod
    def load_model_info(cls):
        """Load model information from model_info.json"""
        try:
            # Try to load from the expected path relative to the working directory
            model_info_path = os.path.join("data", "model_info.json")

            if os.path.exists(model_info_path):
                with open(model_info_path) as f:
                    cls.model_info = json.load(f)
                    cls.supported_output_modes = cls.model_info.get("supportedOutputModes", [])
                    print(
                        f"Loaded model info for {cls.model_info.get('modelName', 'Unknown')} v{cls.model_info.get('version', 'Unknown')}"
                    )
                    print(f"Supported output modes: {cls.supported_output_modes}")
            else:
                print(f"Warning: model_info.json not found at {model_info_path}")
                # Fallback to basic supported modes if file not found
                cls.supported_output_modes = ["HTML", "JSON"]
                cls.model_info = {
                    "modelName": "Unknown",
                    "version": "Unknown",
                    "supportedOutputModes": cls.supported_output_modes,
                }

        except Exception as e:
            print(f"Error loading model_info.json: {str(e)}")
            # Fallback to basic supported modes if loading fails
            cls.supported_output_modes = ["HTML", "JSON"]
            cls.model_info = {
                "modelName": "Unknown",
                "version": "Unknown",
                "supportedOutputModes": cls.supported_output_modes,
            }

    @classmethod
    def get_model_info(cls) -> dict[str, Any]:
        """Get model information"""
        if not cls.model_info:
            cls.load_model_info()
        return cls.model_info

    @classmethod
    def get_supported_output_modes(cls) -> list[str]:
        """Get list of supported output modes for this model"""
        if not cls.supported_output_modes:
            cls.load_model_info()
        return cls.supported_output_modes

    async def predict(self, request: PredictRequest):
        # Ensure model info is loaded
        if not self.__class__.supported_output_modes:
            self.__class__.load_model_info()

        output_mode = request.outputMode
        supported_modes = self.__class__.get_supported_output_modes()

        # Dynamic validation based on model_info.json
        if output_mode not in supported_modes:
            return False, self._handle_unsupported_output(output_mode, supported_modes)

        if not self.__class__.is_initialized:
            return False, self._handle_uninitialized_models()

        # Dynamically construct handler method name and call it
        handler_method_name = f"_handle_{output_mode.lower()}_output"

        try:
            handler = getattr(self, handler_method_name)
            result = await handler(request)
            return True, result
        except AttributeError:
            # Handler method doesn't exist
            return False, HTTPResponse(
                status=501,
                success=False,
                message=f"Output mode '{output_mode}' is listed as supported but handler method '{handler_method_name}' not found",
                error_code="HANDLER_METHOD_NOT_FOUND",
                data={
                    "requestedMode": output_mode,
                    "expectedMethod": handler_method_name,
                    "supportedModes": supported_modes,
                    "modelInfo": self.__class__.get_model_info(),
                },
            ).to_response()
        except NotImplementedError:
            return False, HTTPResponse(
                status=501,
                success=False,
                message=f"Output mode '{output_mode}' is listed as supported but not implemented for this model",
                error_code="OUTPUT_MODE_NOT_IMPLEMENTED",
                data={
                    "requestedMode": output_mode,
                    "supportedModes": supported_modes,
                    "modelInfo": self.__class__.get_model_info(),
                },
            ).to_response()
        except Exception as e:
            return False, HTTPResponse(
                status=500,
                success=False,
                message=f"Error processing {output_mode} output: {str(e)}",
                error_code="PROCESSING_ERROR",
                data={"requestedMode": output_mode, "modelInfo": self.__class__.get_model_info()},
            ).to_response()

    async def _handle_json_output(self, request: PredictRequest):
        raise NotImplementedError("JSON output not implemented for this model")

    async def _handle_ohif_output(self, request: PredictRequest):
        raise NotImplementedError("OHIF annotations output not implemented for this model")

    async def _handle_html_output(self, request: PredictRequest):
        raise NotImplementedError("HTML output not implemented for this model")

    async def _handle_webapp_output(self, request: PredictRequest):
        raise NotImplementedError("Web app output not implemented for this model")

    async def _handle_pdf_output(self, request: PredictRequest):
        raise NotImplementedError("PDF output not implemented for this model")

    def load_model(self, model_weights_path: str):
        """
        Abstract method that must be implemented by child classes
        """
        raise NotImplementedError(
            "Method load_model must be implemented in the custom logic class"
        )

    def stop_model(self):
        """
        Abstract method that must be implemented by child classes
        """

    @classmethod
    def inference(cls, model_input, model_key: str):
        import torch

        try:
            outputs = cls.models[model_key](model_input)
            # Move outputs to CPU and clear GPU memory
            if hasattr(outputs, "detach"):  # Single output
                outputs = outputs.detach().cpu()
            elif isinstance(outputs, list | tuple):  # Multiple outputs
                outputs = [out.detach().cpu() for out in outputs]

            if torch.cuda.is_available():
                torch.cuda.empty_cache()
                del model_input

            return outputs
        except Exception as e:
            print(f"Error during inference: {str(e)}")
            raise

    def _handle_unsupported_output(self):
        return HTTPResponse(
            status=400,
            success=False,
            message="Unsupported output mode",
            error_code="UNSUPPORTED_OUTPUT_MODE",
        ).to_response()

    def _handle_uninitialized_models(self):
        return HTTPResponse(
            status=500,
            success=False,
            message="Models not initialized",
            error_code="MODELS_NOT_INITIALIZED",
        ).to_response()
