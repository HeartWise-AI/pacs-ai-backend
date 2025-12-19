import os
import json
import torch
import base64
import pydicom
import torchvision

import numpy as np

from io import BytesIO

from utils.html_parser import generate_html_report
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService
from models.utils import handle_colorspace, mask_and_crop
from models.echo_prime_view_classifier import EchoPrimeViewClassifier


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        print("Loading model")
        
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return
        try:
            # Load the class mapping from the local package
            class_mapping_path = os.path.join("models", "class_mapping.json")
            with open(class_mapping_path) as fp:
                CustomPredictionService._class_mapping = json.load(fp)

            # Load the HuggingFacemodel config 
            with open(os.path.join("models", "config.json")) as fp:
                CustomPredictionService.model_config = json.load(fp)

            # Create and load the model
            print(f"Model path: {CustomPredictionService.model_config["ModelStateDict"]["model_path"]}")

            print("Loading video encoder")
            view_classifier = EchoPrimeViewClassifier(
                CustomPredictionService.model_config["ModelStateDict"]["model_path"]
            )
            print("Successfully created view classifier model directly from local package")
            CustomPredictionService.models["view_classifier"] = view_classifier
            CustomPredictionService.models["view_classifier"].to(
                "cuda" if torch.cuda.is_available() else "cpu"
            )
            CustomPredictionService.models["view_classifier"].eval()
            CustomPredictionService.is_initialized = True
            print(f"Cuda available: {torch.cuda.is_available()}")
            print("Model loaded")
            
        except Exception as e:
            print(f"Error loading models: {str(e)}")
            raise

    def _run_inference(self, dcm_ds: pydicom.Dataset) -> tuple[str, np.ndarray, str]:
        """
        Run EchoViewClassifier directly on a DICOM dataset, with filtering.
        
        Returns:
            (predicted_class, probabilities, status)
        """
        # 1. Check for pixel data (filters PDFs, SR docs, waveforms)
        try:
            im_array = dcm_ds.pixel_array
        except:
            return None, None, "has no pixel data"
        
        # 2. Handle colorspace
        im_array = handle_colorspace(im_array, dcm_ds)
        
        # 3. Check if video (not image) - must be 4D: [frames, H, W, C]
        if len(im_array.shape) != 4:
            return None, None, "is image"
        
        # 4. Check minimum frame count
        if im_array.shape[0] <= 5:
            return None, None, "too few frames"
        
        # 5. Mask and crop the ultrasound cone
        cropped = mask_and_crop(im_array)
        if isinstance(cropped, str) and cropped == 'failed to detect motion':
            return None, None, "failed to detect motion"
        
        # 6. Resize to 224x224
        cropped_tensor = torch.from_numpy(cropped).permute(0, 3, 1, 2)
        resized = torchvision.transforms.Resize((224, 224))(cropped_tensor)
        
        # 7. Normalize
        mean = [24.277523040771484, 22.14891242980957, 22.404890060424805]
        std = [47.2259521484375, 44.02793502807617, 43.90631103515625]
        video = resized.float()
        video = torchvision.transforms.Normalize(mean, std)(video)
        
        # 8. Sample 16 frames with period=2
        video = video.permute(1, 0, 2, 3)  # [C, F, H, W]
        c, f, h, w = video.shape
        length = 16
        period = 2
        
        if f < length * period:
            video = torch.cat([video, torch.zeros(c, length * period - f, h, w)], dim=1)
        
        indices = period * np.arange(length)
        video = video[:, indices, :, :]
        
        # 9. Run inference
        device = "cuda" if torch.cuda.is_available() else "cpu"
        video = video.unsqueeze(0).to(device)
        
        with torch.no_grad():
            outputs = CustomPredictionService.models["view_classifier"](video)
            probs = torch.softmax(outputs, dim=-1).cpu()
            pred_idx = torch.argmax(probs, dim=-1).item()
            pred_class = CustomPredictionService._class_mapping[pred_idx]
        
        return pred_class, probs.numpy(), "success"

    async def _handle_json_output(self, request: PredictRequest):
        print("Handling JSON output")
        dicoms = []
        try:
            for series_number in request.seriesInstanceImages:
                for instance_number in request.seriesInstanceImages[series_number]:
                    try:
                        instance_data = request.seriesInstanceImages[series_number][instance_number]
                        
                        # Extract image and view from the instance data
                        if isinstance(instance_data, dict):
                            dicom_base64 = instance_data.get("image", instance_data)
                        else:
                            # Backward compatibility: if it's a string, treat it as base64
                            dicom_base64 = instance_data
                        
                        if not self._is_valid_base64(dicom_base64):
                            print(f"Invalid base64 string for series {series_number} instance {instance_number}")
                            continue
                        
                        dicom_data = base64.b64decode(dicom_base64)
                        if not self._is_valid_dicom(dicom_data):
                            print(f"Invalid DICOM data for series {series_number} instance {instance_number}")
                            continue
                        
                        dicom = pydicom.dcmread(BytesIO(dicom_data))
                        dicoms.append(dicom)

                    except Exception as e:
                        error_msg = f"Error in processing series {series_number} instance {instance_number}: {e}"
                        print(error_msg)
                        continue

        except Exception as e:
            error_msg = f"Error in _handle_json_output: {e}"
            print(error_msg)
            return {
                "diagnosis": "Error in _handle_json_output",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in _handle_json_output",
                    "fr": "Erreur dans _handle_json_output",
                    "presentable": True,
                }
            }
        
        try:
            probability: dict[str, float] = self._run_inference(dicoms)
        except Exception as e:
            print(f"Error in _run_inference: {e}")
            return {
                "diagnosis": "Error in _run_inference",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in _run_inference",
                    "fr": "Erreur dans _run_inference",
                    "presentable": True,
                }
            }
        
        return {
            "diagnosis": "report",
            "predictions": "metrics",
            "modelRecommendations": {
                "en": None,
                "fr": None,
                "presentable": True
            }
        }
