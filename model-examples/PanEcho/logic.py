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
from torchvision import tv_tensors
from collections import defaultdict
from torchvision.transforms import v2
from typing import List, Dict, Union, Tuple, Optional

from models.pan_echo import PanEcho
from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):        
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return 
        
        # Instead of downloading at runtime, use the model downloaded during the Docker build.
        try:         
            print("Loading PanEcho model")              
            CustomPredictionService.models['pan_echo'] = PanEcho(
                model_path=config.models['pan_echo'].model_path,
                task_path=config.models['pan_echo'].task_path,
                pretrained=config.models['pan_echo'].pretrained,
                image_encoder_only=config.models['pan_echo'].image_encoder_only,
                backbone_only=config.models['pan_echo'].backbone_only,
                activations=config.models['pan_echo'].activations,  # Use config value (False for proper logit averaging)
                clip_len=config.models['pan_echo'].clip_len,
            )
            print("Successfully created PanEcho model directly from local package")        
        except Exception as e:
            print(f"Error loading PanEcho model: {e}")
            raise e
        
        try:
            print("Setting model to evaluation mode")
            CustomPredictionService.models['pan_echo'].eval()
            CustomPredictionService.models['pan_echo'].to('cuda' if torch.cuda.is_available() else 'cpu')
            CustomPredictionService.is_initialized = True   
        except Exception as e:
            print(f"Error setting model to evaluation mode: {e}")
            raise e
        
        # Keep config in class
        self.config = config
        
        print('Model loaded')

    def _interpret_regression_value(self, value: float, head_cfg: dict, sex: str = None) -> str:
        """Interpret a regression value using clinical thresholds from the mapping.
        
        Args:
            value: The numeric measurement value
            head_cfg: Configuration dict containing normal_range and thresholds
            sex: Optional sex ('male' or 'female') for sex-specific thresholds
            
        Returns:
            Clinical interpretation string (e.g., "Normal", "Mildly abnormal", etc.)
        """
        normal_range = head_cfg.get('normal_range', {})
        thresholds = head_cfg.get('thresholds', {})
        
        def get_range_value(range_dict: dict, key: str) -> Optional[float]:
            if sex and sex in range_dict:
                return range_dict[sex].get(key)
            return range_dict.get(key)
        
        def check_in_range(val: float, range_dict: dict) -> bool:
            if sex and sex in range_dict:
                range_dict = range_dict[sex]
            
            min_val = range_dict.get('min')
            max_val = range_dict.get('max')
            
            if min_val is not None and max_val is not None:
                return min_val <= val <= max_val
            elif min_val is not None:
                return val >= min_val
            elif max_val is not None:
                return val <= max_val
            return False
        
        def is_normal(val: float, normal: dict) -> bool:
            if sex and sex in normal:
                normal = normal[sex]
            
            min_val = normal.get('min')
            max_val = normal.get('max')
            
            if min_val is not None and max_val is not None:
                return min_val <= val <= max_val
            elif min_val is not None:
                return val >= min_val
            elif max_val is not None:
                return val <= max_val
            return True
        
        if normal_range and is_normal(value, normal_range):
            return "Normal"
        
        for threshold_name, threshold_range in thresholds.items():
            if check_in_range(value, threshold_range):
                return threshold_name.replace('_', ' ').title()
        
        if normal_range:
            return "Abnormal"
        
        return "Unknown"

    def postprocess_predictions(self, predictions: Dict[str, Union[float, torch.Tensor]], sex: str = None) -> Tuple[Dict[str, str], Dict[str, Union[float, torch.Tensor]]]:
        """Generate diagnosis labels for each prediction head based on output_class_mapping.json.

        For binary classification heads: use 0.5 threshold to determine class
        For multi-class classification heads: use argmax to get predicted class  
        For regression heads: return value with clinical interpretation using thresholds
        
        Args:
            predictions: Dictionary of model predictions
            sex: Optional patient sex ('male' or 'female') for sex-specific thresholds
        """
        if not hasattr(self.__class__, "_output_class_mapping"):
            mapping_path = os.path.join("models", "output_class_mapping.json")
            with open(mapping_path, "r") as fp:
                self.__class__._output_class_mapping = json.load(fp)
        class_mapping = self.__class__._output_class_mapping

        diagnoses: Dict[str, str] = {}
        serializable_predictions: Dict[str, Union[float, torch.Tensor]] = {}
        
        for head, value in predictions.items():
                            
            head_cfg = class_mapping.get(head, {})
            
            if not head_cfg:
                if isinstance(value, torch.Tensor):
                    scalar_value = value.item()
                else:
                    scalar_value = value
                diagnoses[head] = f"{scalar_value:.3f}"
                serializable_predictions[head] = scalar_value
                continue

            value = value.squeeze(0)
            description = head_cfg.get('description', head)
            units = head_cfg.get('units', '')

            if "regression" in head_cfg:
                if isinstance(value, torch.Tensor):
                    scalar_value = value.item()
                else:
                    scalar_value = value
                
                serializable_predictions[description] = scalar_value
                
                if 'normal_range' in head_cfg or 'thresholds' in head_cfg:
                    interpretation = self._interpret_regression_value(scalar_value, head_cfg, sex)
                    diagnoses[description] = f"{scalar_value:.1f} {units} ({interpretation})"
                else:
                    diagnoses[description] = f"{scalar_value:.1f} {units}"
            else:
                reserved_keys = {'description', 'units', 'unit', 'normal_range', 'thresholds', 
                               'note', 'indexed_normal'}
                class_labels = {k: v for k, v in head_cfg.items() if k not in reserved_keys}
                
                if len(class_labels) == 2:
                    if isinstance(value, torch.Tensor):
                        scalar_value = value.item()
                    else:
                        scalar_value = value

                    threshold = 0.5
                    if scalar_value > threshold:
                        predicted_class = [k for k, v in class_labels.items() if v == 1][0]
                    else:
                        predicted_class = [k for k, v in class_labels.items() if v == 0][0]

                    diagnoses[description] = predicted_class
                else:
                    predicted_class_idx = torch.argmax(value).item()
                    predicted_class = [k for k, v in class_labels.items() if v == predicted_class_idx][0]

                    diagnoses[description] = predicted_class
                    
                    if isinstance(value, torch.Tensor):
                        serializable_predictions[description] = value.tolist()
                    else:
                        serializable_predictions[description] = value

        return diagnoses, serializable_predictions

    def _get_recommendations(self, diagnosis_dict: Dict[str, str], language: str) -> str:
        """Generate clinical recommendations based on echocardiographic findings.
        
        Args:
            predictions: Dictionary of model predictions 
            language: Language code ('en' or 'fr')
            
        Returns:
            Clinical recommendation string
        """
                
        # Analyze key findings
        findings = []
        urgent_findings = []
        
        # Check LV function and size
        if "LV systolic function" in diagnosis_dict:
            lv_function = diagnosis_dict["LV systolic function"]
            if "Moderately" in lv_function or "Severely" in lv_function:
                if "Decreased" in lv_function:
                    urgent_findings.append("LV systolic dysfunction")
        
        if "LV size" in diagnosis_dict:
            lv_size = diagnosis_dict["LV size"]
            if "Moderately" in lv_size or "Severely" in lv_size:
                if "Increased" in lv_size:
                    findings.append("LV enlargement")
        
        # Check diastolic function
        if "LV diastolic function" in diagnosis_dict:
            diastolic = diagnosis_dict["LV diastolic function"]
            if "Moderate" in diastolic or "Severe" in diastolic:
                findings.append("LV diastolic dysfunction")
        
        # Check RV
        if "RV size" in diagnosis_dict:
            rv_size = diagnosis_dict["RV size"]
            if "Moderately" in rv_size or "Severely" in rv_size:
                if "Increased" in rv_size:
                    findings.append("RV enlargement")
        
        # Check LA
        if "Left atrial (LA) size" in diagnosis_dict:
            la_size = diagnosis_dict["Left atrial (LA) size"]
            if "Moderately" in la_size or "Severely" in la_size:
                if "Dilated" in la_size:
                    findings.append("LA enlargement")
        
        # Check valve diseases
        valve_issues = []
        if "Aortic valve stenosis" in diagnosis_dict:
            av_stenosis = diagnosis_dict["Aortic valve stenosis"]
            if "Mild" in av_stenosis or "Moderate" in av_stenosis or "Severe" in av_stenosis:
                valve_issues.append("aortic stenosis")
        
        if "Aortic valve regurgitation" in diagnosis_dict:
            av_regurg = diagnosis_dict["Aortic valve regurgitation"]
            if "Moderate" in av_regurg or "Severe" in av_regurg:
                valve_issues.append("aortic regurgitation")
        
        if "Mitral valve regurgitation" in diagnosis_dict:
            mv_regurg = diagnosis_dict["Mitral valve regurgitation"]
            if "Moderate" in mv_regurg or "Severe" in mv_regurg:
                valve_issues.append("mitral regurgitation")
        
        if "Tricuspid valve regurgitation" in diagnosis_dict:
            tv_regurg = diagnosis_dict["Tricuspid valve regurgitation"]
            if "Moderate" in tv_regurg or "Severe" in tv_regurg:
                valve_issues.append("tricuspid regurgitation")
                
        # Generate recommendations
        if language == "fr":
            if urgent_findings:
                recommendation = "URGENT: Dysfonction systolique du ventricule gauche détectée. "
                recommendation += "Consultation cardiologique immédiate recommandée. "
                
                if findings:
                    findings_text = ", ".join(findings)
                    recommendation += f"Principales anomalies: {findings_text}. "
                
                if valve_issues:
                    valve_text = ", ".join(valve_issues)
                    recommendation += f"Valvulopathies: {valve_text}. "
                
                recommendation += "Recommandations: 1) Consultation cardiologique en urgence, 2) Évaluation de la fonction cardiaque et traitement médical optimal, 3) Surveillance rapprochée."
            
            elif findings or valve_issues:
                recommendation = "Anomalies échocardiographiques détectées. "
                
                if findings:
                    findings_text = ", ".join(findings)
                    recommendation += f"Principales anomalies: {findings_text}. "
                
                if valve_issues:
                    valve_text = ", ".join(valve_issues)
                    recommendation += f"Valvulopathies: {valve_text}. "
                
                recommendation += "Recommandations: 1) Consultation cardiologique dans les 2-4 semaines, 2) Évaluation complète de la fonction cardiaque, 3) Optimisation du traitement médical si indiqué."
            
            else:
                recommendation = "Échocardiographie normale. Surveillance clinique de routine selon les protocoles établis."
        
        else:  # English
            if urgent_findings:
                recommendation = "URGENT: Left ventricular systolic dysfunction detected. "
                recommendation += "Immediate cardiology consultation recommended. "
                
                if findings:
                    findings_text = ", ".join(findings)
                    recommendation += f"Key findings: {findings_text}. "
                
                if valve_issues:
                    valve_text = ", ".join(valve_issues)
                    recommendation += f"Valve abnormalities: {valve_text}. "
                
                recommendation += "Recommendations: 1) Urgent cardiology consultation, 2) Comprehensive cardiac function assessment and optimal medical therapy, 3) Close monitoring."
            
            elif findings or valve_issues:
                recommendation = "Echocardiographic abnormalities detected. "
                
                if findings:
                    findings_text = ", ".join(findings)
                    recommendation += f"Key findings: {findings_text}. "
                
                if valve_issues:
                    valve_text = ", ".join(valve_issues)
                    recommendation += f"Valve abnormalities: {valve_text}. "
                
                recommendation += "Recommendations: 1) Cardiology consultation within 2-4 weeks, 2) Complete cardiac function evaluation, 3) Optimize medical therapy as indicated."
            
            else:
                recommendation = "Normal echocardiogram. Continue routine clinical monitoring per established protocols."
        
        return recommendation
        
    async def _handle_html_output(self, request: PredictRequest):
        try:
            dicoms = []
            for series_number in request.seriesInstanceImages:
                for instance_number in request.seriesInstanceImages[series_number]:
                    instance_data = request.seriesInstanceImages[series_number][instance_number]
                    
                    # Extract image from the instance data (HTML doesn't use views)
                    if isinstance(instance_data, dict):
                        dicom_base64 = instance_data.get("image", instance_data)
                    else:
                        # Backward compatibility: if it's a string, treat it as base64
                        dicom_base64 = instance_data
                    
                    dicoms.append(
                        pydicom.dcmread(
                            BytesIO(
                                base64.b64decode(dicom_base64)
                            )
                        )
                    )

        except Exception as e:
            error_msg = f"Error in _handle_html_output: {e}"
            print(error_msg)
            return {
                "diagnosis": "Error in _handle_html_output",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in _handle_html_output",
                    "fr": "Erreur dans _handle_html_output",
                    "presentable": True,
                }
            }
                
        try:
            probability = self._run_inference(dicoms=dicoms)
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
        print(f"probability: {probability}")
        print(f"probability: {len(probability)}")
        
        # Obtain per-head diagnosis/interpretation
        try:
            diagnosis_dict, predictions_serializable = self.postprocess_predictions(probability)
        except Exception as e:
            print(f"Error in postprocess_predictions: {e}")
            return {
                "diagnosis": "Error in postprocess_predictions",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in postprocess_predictions",
                    "fr": "Erreur dans postprocess_predictions",
                    "presentable": True,
                }
            }
        print(f"predictions_serializable: {predictions_serializable}")
        print(f"diagnosis_dict: {diagnosis_dict}")
        
        # The API schema (`JsonPredictionResponse`) expects the *diagnosis* field
        # to be a **string**.  We therefore serialise the dictionary into a JSON
        # string so that downstream consumers still get a single text field
        # while retaining full information.
        diagnosis = json.dumps(diagnosis_dict)
        
        # Generate recommendations based on stenosis analysis
        recommendations_en = self._get_recommendations(diagnosis_dict, "en")
        recommendations_fr = self._get_recommendations(diagnosis_dict, "fr")

        # Prepare comprehensive data for HTML parser
        html_data = {
            'diagnosis': diagnosis,
            'probability': predictions_serializable,
            'recommendations': {
                'en': recommendations_en,
                'fr': recommendations_fr
            }
        }
        
        try:
            html_output = HTMLParser.generate_detection_results(html_data)
            return {
                'htmlBase64': base64.b64encode(html_output.encode('utf-8')).decode('utf-8')
            }
        except Exception as e:
            print(f"Error in _handle_html_output: {str(e)}")
            raise e

    async def _handle_json_output(self, request: PredictRequest):
        dicoms = []
        views = []
        
        try:
            for series_number in request.seriesInstanceImages:
                for instance_number in request.seriesInstanceImages[series_number]:
                    try:
                        instance_data = request.seriesInstanceImages[series_number][instance_number]
                        
                        # Extract image and view from the instance data
                        if isinstance(instance_data, dict):
                            dicom_base64 = instance_data.get("image", instance_data)
                            view = instance_data.get("view", None)
                        else:
                            # Backward compatibility: if it's a string, treat it as base64
                            dicom_base64 = instance_data
                            view = None
                        
                        if not self._is_valid_base64(dicom_base64):
                            print(f"Invalid base64 string for series {series_number} instance {instance_number}")
                            continue
                        
                        dicom_data = base64.b64decode(dicom_base64)
                        if not self._is_valid_dicom(dicom_data):
                            print(f"Invalid DICOM data for series {series_number} instance {instance_number}")
                            continue
                        
                        dicom = pydicom.dcmread(BytesIO(dicom_data))
                        dicoms.append(dicom)
                        views.append(view)

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

        if len(dicoms) != len(views):
            return {
                "diagnosis": "Number of dicoms and views must be the same",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Number of dicoms and views must be the same",
                    "fr": "Le nombre de dicoms et de vues doit être le même",
                    "presentable": True,
                }
            }

        try:
            probability = self._run_inference(dicoms=dicoms, views=views)
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
        print(f"probability: {probability}")
        # Obtain per-head diagnosis/interpretation
        try:
            diagnosis_dict, predictions_serializable = self.postprocess_predictions(probability)
        except Exception as e:
            print(f"Error in postprocess_predictions: {e}")
            return {
                "diagnosis": "Error in postprocess_predictions",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in postprocess_predictions",
                    "fr": "Erreur dans postprocess_predictions",
                    "presentable": True,
                }
            }
        print(f"diagnosis_dict: {diagnosis_dict}")
        # Generate recommendations
        try:
            recommendations_en = self._get_recommendations(diagnosis_dict, "en")
            recommendations_fr = self._get_recommendations(diagnosis_dict, "fr")
        except Exception as e:
            print(f"Error in _get_recommendations: {e}")
            recommendations_en = "Error in _get_recommendations"
            recommendations_fr = "Error in _get_recommendations"

        return {
            'diagnosis': json.dumps(diagnosis_dict),
            'predictions': predictions_serializable,
            'modelRecommendations': {
                'en': recommendations_en,
                'fr': recommendations_fr,
                'presentable': True
            }
        }

    def _load_clip_from_dicom(
        self, 
        pixel_array: np.ndarray, 
        clip_len: int = 16
    ):
        """
        Load and preprocess video clip from DICOM pixel array.
        Adapted from EchoDataset._load_clip method.
        """
        # CRITICAL: Ensure pixel values are in [0, 255] range for proper normalization
        if pixel_array.dtype == np.uint16 or pixel_array.max() > 255:
            # Scale to [0, 255] range for 16-bit or out-of-range values
            pix_min, pix_max = pixel_array.min(), pixel_array.max()
            if pix_max > pix_min:
                pixel_array = ((pixel_array - pix_min) / (pix_max - pix_min) * 255).astype(np.uint8)
            else:
                pixel_array = np.zeros_like(pixel_array, dtype=np.uint8)
        elif pixel_array.dtype != np.uint8:
            # Ensure uint8 type
            pixel_array = pixel_array.astype(np.uint8)
        
        # Handle different input dimensions
        if pixel_array.ndim == 3:
            # Single channel to 3 channels (grayscale to RGB)
            pixel_array = np.expand_dims(pixel_array, axis=-1)  # Shape: F,W,H,1
            pixel_array = np.repeat(pixel_array, 3, axis=-1)    # Shape: F,W,H,3
        elif pixel_array.ndim == 4:
            # Already in correct format (F,W,H,C)
            pass
        else:
            raise ValueError(f"Invalid pixel array dimensions: {pixel_array.shape}")
        
        frame_count = pixel_array.shape[0]
        
        # Single clip sampling
        if frame_count < clip_len:
            start_idx = 0
        else:
            # For inference, use deterministic center sampling
            start_idx = (frame_count - clip_len) // 2
        
        end_idx = min(start_idx + clip_len, frame_count)
        clip = pixel_array[start_idx:end_idx]
        
        # Pad if necessary
        if clip.shape[0] < clip_len:
            padding = np.zeros((clip_len - clip.shape[0], *clip.shape[1:]), dtype=clip.dtype)
            clip = np.concatenate([clip, padding], axis=0)
        
        return clip

    def _create_transform(self, normalization: str = ''):
        """
        Create transform pipeline based on normalization settings.
        Adapted from EchoDataset transform creation.
        """
        
        transform_list = [
            v2.Resize(size=(256, 256)),
            v2.CenterCrop(size=(224, 224)),
            v2.ToDtype(torch.float32, scale=True)
        ]

        # Add normalization
        if normalization == 'imagenet':
            print("Using ImageNet normalization")
            mean = np.array([0.485, 0.456, 0.406])
            std = np.array([0.229, 0.224, 0.225])
        elif normalization == 'kinetics':
            mean = np.array([0.43216, 0.394666, 0.37645])
            std = np.array([0.22803, 0.22145, 0.216989])
        else:
            mean = np.array([0.48145466, 0.4578275, 0.40821073])
            std = np.array([0.26862954, 0.26130258, 0.27577711])
        
        transform_list.append(v2.Normalize(mean=mean, std=std))
        
        return v2.Compose(transform_list)

    def _run_inference_logits_only(self, dicoms: List[pydicom.Dataset]) -> Dict[str, torch.Tensor]:
        """
        Run inference and return averaged logits without applying activations.
        Processes at most 8 DICOMs in a single batch.
        
        Args:
            dicoms: List of DICOM datasets
            
        Returns:
            Dictionary of averaged logits (before activation)
        """
        try:
            # Create transform pipeline
            transform = self._create_transform(normalization=self.config.models['pan_echo'].normalization)
            device = 'cuda' if torch.cuda.is_available() else 'cpu'
            MAX_BATCH_SIZE = 8
            
            # Process at most MAX_BATCH_SIZE DICOMs
            batch_size = min(MAX_BATCH_SIZE, len(dicoms))
            batch_clips = []
                        
            for i in range(batch_size):
                pixel_array: np.ndarray = dicoms[i].pixel_array
                
                # Skip invalid data
                if pixel_array.ndim < 3:
                    print(f"Skipping DICOM {i} with invalid dimensions: {pixel_array.shape}")
                    continue
                                
                # Load clip using improved method
                clip = self._load_clip_from_dicom(
                    pixel_array, 
                    clip_len=self.config.models['pan_echo'].clip_len 
                )
                
                # Convert to torch tensor and apply transforms
                clip_tensor = tv_tensors.Video(np.transpose(clip, (0, 3, 1, 2)))
                clip_tensor = transform(clip_tensor)
                
                # (F, C, H, W) -> (C, F, H, W) for model input
                clip_tensor = torch.permute(clip_tensor, (1, 0, 2, 3))
                
                batch_clips.append(clip_tensor)
            
            if not batch_clips:
                raise ValueError("No valid DICOM files processed")
            
            # Stack clips into a batch tensor: (batch_size, C, F, H, W)
            batch_tensor = torch.stack(batch_clips).to(device)
            
            # Run inference on this batch
            with torch.no_grad():
                batch_outputs: Dict[str, torch.Tensor] = CustomPredictionService.models['pan_echo'](batch_tensor)
            
            # Average the logits across the batch dimension (without activation)
            averaged_logits = {}
            for key, output_tensor in batch_outputs.items():
                # Output tensor shape: (batch_size, ...)
                # Average across batch dimension (dim=0)
                averaged_logits[key] = torch.mean(output_tensor, dim=0)
                        
            return averaged_logits
        
        except Exception as e:
            # Clean up GPU memory
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            print(f"Error in _run_inference_logits_only: {str(e)}")
            raise e

    def _run_inference(
        self, 
        dicoms: List[pydicom.Dataset], 
        views: Optional[List[Optional[str]]] = None
    ) -> Dict[str, torch.Tensor]:
        """
        Run inference on DICOM datasets and return predictions with activations applied.
        
        Computes averaged logits via _run_inference_logits_only, then applies task-specific
        activation functions to produce final predictions.
        
        Args:
            dicoms: List of DICOM datasets to process
            views: Optional list of view names (currently unused, reserved for future use)
            
        Returns:
            Dictionary mapping task names to predictions with activations:
            - Binary classification: sigmoid probabilities (0-1)
            - Multi-class classification: softmax probabilities
            - Regression: raw values
        """
        
        final_logits = self._run_inference_logits_only(dicoms)
        
        final_output = {}
        task_dict = CustomPredictionService.models['pan_echo'].model.tasks
        
        for key, averaged_logits in final_logits.items():
            # Find the task to determine the correct activation
            task = next((t for t in task_dict if t.task_name == key), None)
            if task:
                if task.task_type == 'binary_classification':
                    final_output[key] = torch.sigmoid(averaged_logits)
                elif task.task_type == 'multi-class_classification':
                    # Apply softmax to averaged logits
                    final_output[key] = torch.softmax(averaged_logits, dim=-1)
                else:
                    # Regression - no activation needed
                    final_output[key] = averaged_logits
            else:
                # Fallback if task not found
                final_output[key] = averaged_logits
        
        return final_output