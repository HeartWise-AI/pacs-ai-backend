import base64
import pydicom

from io import BytesIO
from PIL import Image
from typing import List

from model.MedGemma import MedGemma
from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService

DEFAULT_PROMPT = "Describe this medical image"


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):        
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return 
        
        try:         
            print("Loading MedGemma model")
            model_path = config.models['medgemma'].model_path
            CustomPredictionService.models['medgemma'] = MedGemma(model_path=model_path)
            CustomPredictionService.is_initialized = True
            print("Successfully loaded MedGemma model")
        except Exception as e:
            print(f"Error loading MedGemma model: {e}")
            raise e
        
        self.config = config
        print('Model loaded')

    def _extract_frame_from_dicom(self, dicom: pydicom.Dataset) -> Image.Image:
        """Extract middle frame from a DICOM dataset."""
        pixel_array = dicom.pixel_array
        
        if pixel_array.ndim >= 3:
            frame = pixel_array[pixel_array.shape[0] // 2]
        else:
            frame = pixel_array
        
        return Image.fromarray(frame)

    def _run_inference(self, images: List[Image.Image], prompt: str = None) -> str:
        """Run MedGemma inference on images.
        
        Args:
            images: List of PIL Images to analyze
            prompt: Optional custom prompt (uses DEFAULT_PROMPT if not provided)
            
        Returns:
            Generated text response from MedGemma
        """
        if not images:
            return "No valid images provided for analysis."
        
        if prompt is None:
            prompt = DEFAULT_PROMPT
        
        image = images[0]
        
        return CustomPredictionService.models['medgemma'].generate(
            prompt=prompt,
            image=image
        )

    async def _handle_html_output(self, request: PredictRequest):
        if not request.seriesInstanceImages:
            return {
                "diagnosis": "No images provided",
                "predictions": {},
                "modelRecommendations": {
                    "en": "No images provided for analysis",
                    "fr": "Aucune image fournie pour l'analyse",
                    "presentable": True,
                }
            }

        try:
            images = []
            for series_number in request.seriesInstanceImages:
                for instance_number in request.seriesInstanceImages[series_number]:
                    dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                    dicom = pydicom.dcmread(BytesIO(base64.b64decode(dicom_base64)))
                    images.append(self._extract_frame_from_dicom(dicom))

        except Exception as e:
            print(f"Error in _handle_html_output: {e}")
            return {
                "diagnosis": "Error processing DICOM files",
                "predictions": {},
                "modelRecommendations": {
                    "en": f"Error: {e}",
                    "fr": f"Erreur: {e}",
                    "presentable": True,
                }
            }
        
        diagnosis = self._run_inference(images)
        
        html_data = {
            'diagnosis': diagnosis,
            'probability': {},
            'recommendations': {
                'en': diagnosis,
                'fr': diagnosis
            }
        }
        
        try:
            html_output = HTMLParser.generate_detection_results(html_data)
            return {
                'htmlBase64': base64.b64encode(html_output.encode('utf-8')).decode('utf-8')
            }
        except Exception as e:
            print(f"Error generating HTML: {e}")
            raise e

    async def _handle_json_output(self, request: PredictRequest):
        if not request.seriesInstanceImages:
            return {
                "diagnosis": "No images provided",
                "predictions": {},
                "modelRecommendations": {
                    "en": "No images provided for analysis",
                    "fr": "Aucune image fournie pour l'analyse",
                    "presentable": True,
                }
            }

        images = []

        try:
            for series_number in request.seriesInstanceImages:
                for instance_number in request.seriesInstanceImages[series_number]:
                    try:
                        dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                        
                        if not self._is_valid_base64(dicom_base64):
                            print(f"Invalid base64 for series {series_number} instance {instance_number}")
                            continue
                        
                        dicom_data = base64.b64decode(dicom_base64)
                        if not self._is_valid_dicom(dicom_data):
                            print(f"Invalid DICOM for series {series_number} instance {instance_number}")
                            continue
                        
                        dicom = pydicom.dcmread(BytesIO(dicom_data))
                        images.append(self._extract_frame_from_dicom(dicom))

                    except Exception as e:
                        print(f"Error processing series {series_number} instance {instance_number}: {e}")
                        continue

        except Exception as e:
            print(f"Error in _handle_json_output: {e}")
            return {
                "diagnosis": "Error processing DICOM files",
                "predictions": {},
                "modelRecommendations": {
                    "en": f"Error: {e}",
                    "fr": f"Erreur: {e}",
                    "presentable": True,
                }
            }

        diagnosis = self._run_inference(images)

        return {
            'diagnosis': diagnosis,
            'predictions': {},
            'modelRecommendations': {
                'en': diagnosis,
                'fr': diagnosis,
                'presentable': True
            }
        }