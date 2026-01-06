import configparser
import os
import uuid
import json
import torch
import base64
import pydicom
import cv2 as cv
import numpy as np
import torch.nn as nn

from io import BytesIO
from typing import List
from torchvision.transforms import v2

from models.vaso_vision import VasoVision

from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        print('Loading model...')
        
        CustomPredictionService.config = config

        # Create and load the model
        try:
            CustomPredictionService.model_path = os.path.join(
                'models', CustomPredictionService.config.model_path.path
            )
            print(f"Model path: {CustomPredictionService.model_path}")
            print(f"Head structure: {CustomPredictionService.config.head_structure}")

            CustomPredictionService.models['vaso_vision'] = VasoVision(
                CustomPredictionService.config.model_path,
                CustomPredictionService.config.head_structure
            )
            CustomPredictionService.is_initialized = True
            print('Model loaded')
        except Exception as e:
            print(f"Error loading model: {e}")
            raise e  

    def _is_valid_base64(self, dicom_base64):
        try:
            if isinstance(dicom_base64, str):
                base64.b64decode(dicom_base64)
                return True
            return False
        except Exception as e:
            return False
 
    def _process_predictions(self, probability):
        class_mapping = CustomPredictionService.config.class_mapping
        
        threshold = class_mapping['threshold']
        class_mapping = {v: k for v, k in class_mapping.items() if v != 'threshold'} 
        
        processed_predictions = {'probability': probability}
                
        if probability < threshold:
            processed_predictions['class'] = class_mapping['normal']
        else:
            processed_predictions['class'] = class_mapping['reduced']
        
        return processed_predictions
        
    def _is_valid_dicom(self, dicom):
        """Check if bytes represent valid DICOM data."""
        try:
            # Check for DICOM magic bytes
            if len(dicom) < 132:
                return False
            
            # DICOM files start with specific bytes
            if dicom[:4] == b'DICM':
                return True
            
            # Check for transfer syntax in first 132 bytes
            if dicom[128:132] in [b'DICM', b'DICM']:
                return True
                
            return False
        except Exception:
            return False
    
    async def _handle_json_output(self, request: PredictRequest):
        dicoms = []

        try:
            for series_number in request.seriesInstanceImages:
                for instance_number in request.seriesInstanceImages[series_number]:
                    try: 
                        dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                        
                        # Validat base64 string
                        if not self._is_valid_base64(dicom_base64):
                            print(f"Invalid base64 string for series {series_number} instance {instance_number}")
                            continue
                        
                        # Decode base64 string and validate DICOM data
                        dicom_data = base64.b64decode(dicom_base64)
                        if not self._is_valid_dicom(dicom_data):
                            print(f"Invalid DICOM data for series {series_number} instance {instance_number}")
                            continue
                                            
                        # Load DICOM
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
            probability: float = self._run_inference(dicoms)
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

        # Obtain per-head diagnosis/interpretation
        try:
            structured_predictions: dict[str, dict] = self._process_predictions(probability)
        except Exception as e:
            print(f"Error in _process_predictions: {e}")
            structured_predictions = {
                "probability": probability,
                "class": "Error in _process_predictions",
            }
        
        # Transform into a diagnosis string
        try:    
            diagnosis: str = self._get_diagnosis(structured_predictions)
        except Exception as e:
            print(f"Error in _get_diagnosis: {e}")
            diagnosis: str = "Error in _get_diagnosis"

        try:
            # Generate recommendations based on stenosis analysis
            recommendations_en = self._get_recommendations(structured_predictions, "en")
            recommendations_fr = self._get_recommendations(structured_predictions, "fr")            
        except Exception as e:
            print(f"Error in _get_recommendations: {e}")
            recommendations_en = "Error in _get_recommendations"
            recommendations_fr = "Error in _get_recommendations"
        
        return {
            "diagnosis": diagnosis,
            "predictions": structured_predictions,  # Use the dict, not the JSON string
            "modelRecommendations": {
                "en": recommendations_en,
                "fr": recommendations_fr,
                "presentable": True,
            },
        }

    def _videoShenanigans(self, video):
        # Use uuid.uuid4() to create a unique file name
        unique_filename = f"tmp_{uuid.uuid4()}.avi"
        compressedVideo = []
        fourcc = cv.VideoWriter_fourcc("M", "J", "P", "G")
        out = cv.VideoWriter(unique_filename, fourcc, 15, video.shape[1:3])
        try:
            for i in video:
                out.write(i)
            out.release()
        except:
            print("Error in writing video file")
            # Ensure file is deleted even if an error occurs
            if os.path.exists(unique_filename):
                os.remove(unique_filename)
            # Re-raise the exception to handle it as needed by the caller
            raise

        capture = cv.VideoCapture(unique_filename)
        frame_count = int(capture.get(cv.CAP_PROP_FRAME_COUNT))
        try:
            for count in range(frame_count):
                ret, frame = capture.read()
                if not ret:
                    raise ValueError(f"Failed to load frame #{count} of video.")
                frame = cv.cvtColor(frame, cv.COLOR_BGR2RGB)
                compressedVideo.append(frame)
        finally:
            capture.release()

            # Delete the temporary file after processing
            if os.path.exists(unique_filename):
                os.remove(unique_filename)

        return np.asarray(compressedVideo).transpose(0, 3, 1, 2)

    def _run_inference(self, dicoms: List[pydicom.Dataset]) -> float:
        try:
            mean_output: float = 0.0
            for dicom in dicoms:
                pixel_array: np.ndarray = dicom.pixel_array
                if pixel_array.ndim == 1 or pixel_array.ndim == 2:
                    continue
                
                if pixel_array.ndim == 3:
                    # Expand single channel to 3 channels by repeating
                    pixel_array = np.expand_dims(pixel_array, axis=1)  # Shape: F,1,W,H
                    pixel_array = np.repeat(pixel_array, 3, axis=1)    # Shape: F,3,W,H
                    
                assert pixel_array.ndim == 4, "Pixel array must have 4 dimensions"
                
                pixel_array = self._videoShenanigans(pixel_array.transpose(0, 2, 3, 1)).astype(np.uint8)
                pixel_array = pixel_array.astype(np.float32)
                
                mean = [112.24039459228516, 112.24039459228516, 112.24039459228516]
                std = [39.012229919433594, 39.012229919433594, 39.012229919433594]
                
                # Convert numpy array to torch tensor and ensure float type
                video = torch.from_numpy(pixel_array)
                video = v2.Resize((256, 256), antialias=None)(video)
                video = v2.Normalize(mean, std)(video)
                video = video.permute(1, 0, 2, 3)
                video = video.numpy()
                
                c, f, h, w = video.shape
                length = 72
                if f < length * 2:
                    video = np.concatenate((video, np.zeros((c, length * 2 - f, h, w), video.dtype)), axis=1)
                    c, f, h, w = video.shape
                start = np.array([0])
                video = tuple(video[:, s + 2 * np.arange(length), :, :] for s in start)[0]
                
                video = torch.from_numpy(video)

                # Add batch dimension after processing
                video = video.unsqueeze(0).to('cuda' if torch.cuda.is_available() else 'cpu')
                with torch.no_grad():
                    output: torch.Tensor = CustomPredictionService.models['x3d_m'](video)
                    output = torch.sigmoid(output)  # Add sigmoid activation
                    output: float = output.squeeze(0).detach().cpu().numpy().astype(float)
                    mean_output += float(output[0])
                    
            mean_output = mean_output / len(dicoms)
            return float(mean_output)
        
        except Exception as e:
            print(f"Error in handler: {e}")
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise e
            return 0.0
        
    def _get_recommendations(self, structured_predictions: dict[str, dict], language: str) -> dict[str, str]:
        class_mapping = CustomPredictionService.config.class_mapping
            
        if structured_predictions['class'] == class_mapping['normal']:
            return "No recommendations needed" if language == "en" else "Aucune recommandation nécessaire"
        
        return "Please consult a physician for further evaluation" if language == "en" else "Veuillez consulter un médecin pour une évaluation plus approfondie"
        
    def _get_diagnosis(self, structured_predictions: dict[str, dict]) -> str:
        class_mapping = CustomPredictionService.config.class_mapping
        class_mapping = {v: k for v, k in class_mapping.items() if v != 'threshold'} 
        
        if structured_predictions['class'] == class_mapping['normal']:
            return "Normal Right Ventricular Function"
        
        return "Reduced Right Ventricular Function"