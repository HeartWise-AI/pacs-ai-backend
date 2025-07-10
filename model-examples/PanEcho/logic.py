import os
import uuid
import torch
import base64
import pydicom
import cv2 as cv
import numpy as np
import torch.nn as nn
import json

from io import BytesIO
from typing import List, Dict, Union, Tuple
from torchvision import tv_tensors
from torchvision.transforms import v2

from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService

from models.pan_echo import PanEcho

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

    def postprocess_predictions(self, predictions: Dict[str, Union[float, torch.Tensor]]) -> Tuple[Dict[str, str], Dict[str, Union[float, torch.Tensor]]]:
        """Generate diagnosis labels for each prediction head based on output_class_mapping.json.

        For binary classification heads: use 0.5 threshold to determine class
        For multi-class classification heads: use argmax to get predicted class  
        For regression heads: return raw value with units
        """
        # ------------------------------------------------------------------
        # 1) Lazy-load the output class mapping file (only once per process)
        # ------------------------------------------------------------------
            
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
                # If head not found in mapping, just return the raw value
                if isinstance(value, torch.Tensor):
                    scalar_value = value.item()
                else:
                    scalar_value = value
                diagnoses[head] = f"{scalar_value:.3f}"
                serializable_predictions[head] = scalar_value
                continue

            value = value.squeeze(0) # remove batch dimension
            description = head_cfg.get('description', head)

            # Check if this is a regression task
            if "regression" in head_cfg:
                # Convert tensor to scalar for regression
                if isinstance(value, torch.Tensor):
                    scalar_value = value.item()
                else:
                    scalar_value = value
                
                units = head_cfg.get("units", "")
                if units:
                    diagnoses[description] = f"{scalar_value:.1f} {units}"
                else:
                    diagnoses[description] = f"{scalar_value:.1f}"
                serializable_predictions[description] = scalar_value

            else:
                # Classification task - get all class labels (excluding 'description')
                class_labels = {k: v for k, v in head_cfg.items() if k != "description"}
                
                if len(class_labels) == 2:
                    # Binary classification - convert to scalar and use 0.5 threshold
                    if isinstance(value, torch.Tensor):
                        scalar_value = value.item()
                    else:
                        scalar_value = value

                    threshold = 0.5
                    if scalar_value > threshold:
                        # Find the class with value 1
                        predicted_class = [k for k, v in class_labels.items() if v == 1][0]
                    else:
                        # Find the class with value 0
                        predicted_class = [k for k, v in class_labels.items() if v == 0][0]
                    diagnoses[description] = predicted_class
                    serializable_predictions[description] = scalar_value
                else:
                    # Multi-class classification - use argmax on tensor                   
                    predicted_class_idx = torch.argmax(value).item()
                    
                    # Find the class name corresponding to this index
                    predicted_class = [k for k, v in class_labels.items() if v == predicted_class_idx][0]
                    diagnoses[description] = predicted_class
                    
                    # For multi-class, store the raw probability/logits as serializable
                    if isinstance(value, torch.Tensor):
                        serializable_predictions[description] = value.tolist()  # Convert tensor to list for multi-class
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
        dicoms = []
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                dicoms.append(
                    pydicom.dcmread(
                        BytesIO(
                            base64.b64decode(dicom_base64)
                        )
                    )
                )
                
        probability = self._run_inference(dicoms)
        
        # Obtain per-head diagnosis/interpretation
        diagnosis_dict, predictions_serializable = self.postprocess_predictions(probability)

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
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                dicoms.append(
                    pydicom.dcmread(
                        BytesIO(
                            base64.b64decode(dicom_base64)
                        )
                    )
                )
        
        probability = self._run_inference(dicoms)
        
        # Obtain per-head diagnosis/interpretation
        diagnosis_dict, predictions_serializable = self.postprocess_predictions(probability)
        
        # Generate recommendations
        recommendations_en = self._get_recommendations(diagnosis_dict, "en")
        recommendations_fr = self._get_recommendations(diagnosis_dict, "fr")
        
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
        # Handle different input dimensions
        if pixel_array.ndim == 3:
            # Single channel to 3 channels
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

    def _run_inference(self, dicoms: List[pydicom.Dataset]) -> Dict[str, torch.Tensor]:
        """
        Enhanced inference function that processes each DICOM individually and averages results.
        """
        try:
            # Create transform pipeline
            transform = self._create_transform(normalization=self.config.models['pan_echo'].normalization)
            
            # Store individual results
            all_outputs = []
            
            # Process each DICOM individually
            for i, dicom in enumerate(dicoms):
                pixel_array: np.ndarray = dicom.pixel_array
                
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
                # Single clip: (F, H, W, C) -> (F, C, H, W)
                clip_tensor = tv_tensors.Video(np.transpose(clip, (0, 3, 1, 2)))
                clip_tensor = transform(clip_tensor)
                # (F, C, H, W) -> (C, F, H, W) for model input
                clip_tensor = torch.permute(clip_tensor, (1, 0, 2, 3))
                
                # Add batch dimension for single video
                clip_tensor = clip_tensor.unsqueeze(0)  # Shape: (1, C, F, H, W)
                
                # Move to device
                device = 'cuda' if torch.cuda.is_available() else 'cpu'
                clip_tensor = clip_tensor.to(device)
                
                print(f"Processing DICOM {i} with shape: {clip_tensor.shape}")
                
                # Run inference on single DICOM
                with torch.no_grad():
                    single_output: Dict[str, torch.Tensor] = CustomPredictionService.models['pan_echo'](clip_tensor)
                
                all_outputs.append(single_output)
            
            if not all_outputs:
                raise ValueError("No valid DICOM files processed")
            
            # Average the results across all DICOMs
            averaged_output = {}
            for key in all_outputs[0].keys():
                # Stack all outputs for this key
                stacked_outputs = torch.stack([output[key] for output in all_outputs])
                # Average across the batch dimension
                averaged_output[key] = torch.mean(stacked_outputs, dim=0)
                        
            return averaged_output
        
        except Exception as e:
            # Clean up GPU memory
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            print(f"Error in _run_inference: {str(e)}")
            raise e