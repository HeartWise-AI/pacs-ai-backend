import os
import json
import torch
import base64
import pydicom
import numpy as np
import torchvision

from io import BytesIO
from torchvision import tv_tensors
from torchvision.transforms import v2
from typing import Any, List, Dict, Union, Tuple, Optional

from models.pan_echo import PanEcho
from models.echo_prime_view_classifier import EchoPrimeViewClassifier
from models.view_classifier_utils import handle_colorspace, mask_and_crop
from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService

# Selected views for PanEcho inference (includes zoom variants via substring match)
SELECTED_VIEWS = {"A2C", "A4C", "A5C", "PLAX", "PSAX"}


def matches_selected_view(pred_class: Optional[str]) -> bool:
    """Check if the predicted view class matches any of the selected views.
    
    Uses substring matching to accept zoom variants (e.g., A4C_ZOM matches A4C).
    
    Args:
        pred_class: The predicted view class string
        
    Returns:
        True if the view matches any selected view, False otherwise
    """
    if pred_class is None:
        return False
    return any(view in pred_class for view in SELECTED_VIEWS)


class CustomPredictionService(BasePredictionService):
    _view_classifier_batch_size = 8

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
        except Exception as e:
            print(f"Error setting model to evaluation mode: {e}")
            raise e

        view_classifier_weights_path = config.models["pan_echo"].view_classifier_model_path
        class_mapping_path = (
            config.models["pan_echo"].view_classifier_class_mapping_path
            or os.path.join("models", "view_classifier_class_mapping.json")
        )
        try:
            if class_mapping_path and os.path.exists(class_mapping_path):
                with open(class_mapping_path, "r") as fp:
                    CustomPredictionService._view_classifier_class_mapping = json.load(fp)
            else:
                print(f"View classifier class mapping not found at {class_mapping_path}")

            if view_classifier_weights_path and os.path.exists(view_classifier_weights_path):
                print("Loading PanEcho view classifier")
                view_classifier = EchoPrimeViewClassifier(view_classifier_weights_path)
                view_classifier.eval()
                view_classifier.to("cuda" if torch.cuda.is_available() else "cpu")
                CustomPredictionService.models["view_classifier"] = view_classifier
                print("Successfully loaded PanEcho view classifier")
            else:
                print(
                    "View classifier weights not found; PanEcho will fall back to "
                    "request metadata or unfiltered inference"
                )
        except Exception as e:
            print(f"Error loading view classifier, continuing without it: {e}")

        CustomPredictionService.is_initialized = True   
        
        # Keep config in class
        self.config = config
        
        print('Model loaded')

    def _is_valid_base64(self, dicom_base64):
        try:
            if isinstance(dicom_base64, str):
                base64.b64decode(dicom_base64)
                return True
            return False
        except Exception:
            return False

    def _is_valid_dicom(self, dicom):
        try:
            if len(dicom) < 132:
                return False
            if dicom[128:132] == b"DICM":
                return True
            if dicom[:4] == b"DICM":
                return True
            return False
        except Exception:
            return False

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

    def _filter_dicoms_with_metadata(
        self, 
        dicoms: List[pydicom.Dataset], 
        metadata: dict
    ) -> List[pydicom.Dataset]:
        """Filter DICOMs to keep only those with selected echocardiographic views.
        
        Uses metadata (keyed by SeriesInstanceUID) to get the predicted_class (view)
        for each DICOM and filters based on SELECTED_VIEWS.
        
        Args:
            dicoms: List of DICOM datasets
            metadata: Dict keyed by SeriesInstanceUID containing view predictions
            
        Returns:
            List of filtered DICOMs matching selected views
        """
        filtered_dicoms = []
        
        for dicom in dicoms:
            dicom_name = str(dicom.SeriesInstanceUID)
            if dicom_name not in metadata:
                continue
            dicom_meta = metadata[dicom_name]
            pred_class = dicom_meta.get('predicted_class')
            if not matches_selected_view(pred_class):
                continue
            filtered_dicoms.append(dicom)
        
        return filtered_dicoms

    def _prepare_view_classifier_video(
        self, dicom: pydicom.Dataset
    ) -> Tuple[Optional[torch.Tensor], str]:
        try:
            im_array = dicom.pixel_array
        except Exception:
            return None, "has no pixel data"

        im_array = handle_colorspace(im_array, dicom)

        if len(im_array.shape) != 4:
            return None, "is image"

        if im_array.shape[0] <= 5:
            return None, "too few frames"

        cropped = mask_and_crop(im_array)
        if isinstance(cropped, str) and cropped == "failed to detect motion":
            return None, "failed to detect motion"

        cropped_tensor = torch.from_numpy(cropped).permute(0, 3, 1, 2)
        resized = torchvision.transforms.Resize((224, 224))(cropped_tensor)

        mean = [24.277523040771484, 22.14891242980957, 22.404890060424805]
        std = [47.2259521484375, 44.02793502807617, 43.90631103515625]
        video = resized.float()
        video = torchvision.transforms.Normalize(mean, std)(video)

        video = video.permute(1, 0, 2, 3)
        c, f, h, w = video.shape
        length = 16
        period = 2

        if f < length * period:
            video = torch.cat([video, torch.zeros(c, length * period - f, h, w)], dim=1)

        indices = period * np.arange(length)
        video = video[:, indices, :, :]

        return video, "success"

    def _run_view_classifier_batch_inference(
        self, prepared_videos: List[torch.Tensor]
    ) -> List[Tuple[str, np.ndarray, str]]:
        if not prepared_videos:
            return []

        device = "cuda" if torch.cuda.is_available() else "cpu"
        batch_size = self._view_classifier_batch_size
        results: List[Tuple[str, np.ndarray, str]] = []

        for batch_start in range(0, len(prepared_videos), batch_size):
            batch_videos = prepared_videos[batch_start:batch_start + batch_size]
            batch_tensor = torch.stack(batch_videos).to(device)

            with torch.no_grad():
                outputs = CustomPredictionService.models["view_classifier"](batch_tensor)
                probs = torch.softmax(outputs, dim=-1).cpu()
                pred_indices = torch.argmax(probs, dim=-1).tolist()

            for pred_idx, prob in zip(pred_indices, probs.numpy()):
                pred_class = CustomPredictionService._view_classifier_class_mapping[str(pred_idx)]
                results.append((pred_class, prob, "success"))

        return results

    def _generate_view_classifier_metadata(
        self, dicoms: List[pydicom.Dataset]
    ) -> Optional[Dict[str, Dict[str, Any]]]:
        if "view_classifier" not in CustomPredictionService.models:
            return None
        if not hasattr(CustomPredictionService, "_view_classifier_class_mapping"):
            return None

        metadata: Dict[str, Dict[str, Any]] = {}
        prepared_videos: List[torch.Tensor] = []
        prepared_dicom_names: List[str] = []

        for dicom in dicoms:
            dicom_name = str(dicom.SeriesInstanceUID)
            video, status = self._prepare_view_classifier_video(dicom)
            if video is None:
                metadata[dicom_name] = {
                    "predicted_class": None,
                    "probabilities": None,
                    "status": status,
                }
                continue

            prepared_dicom_names.append(dicom_name)
            prepared_videos.append(video)

        batch_results = self._run_view_classifier_batch_inference(prepared_videos)

        for dicom_name, (pred_class, probs, status) in zip(prepared_dicom_names, batch_results):
            metadata[dicom_name] = {
                "predicted_class": pred_class,
                "probabilities": probs.tolist() if probs is not None else None,
                "status": status,
            }

        return metadata

    def _prepare_dicoms_for_inference(
        self, dicoms: List[pydicom.Dataset], request_metadata: Optional[Dict[str, Any]]
    ) -> List[pydicom.Dataset]:
        metadata = request_metadata
        metadata_source = "request.additionalMetadata"

        if not metadata:
            metadata = self._generate_view_classifier_metadata(dicoms)
            metadata_source = "PanEcho view classifier"

        if metadata:
            filtered_dicoms = self._filter_dicoms_with_metadata(dicoms, metadata)
            print(
                f"Filtering via {metadata_source}: {len(dicoms)} total DICOMs, "
                f"{len(filtered_dicoms)} matched selected views "
                f"({', '.join(sorted(SELECTED_VIEWS))})"
            )
            if filtered_dicoms:
                return filtered_dicoms
            print(f"No selected view matches from {metadata_source}, using all {len(dicoms)} DICOMs")
        else:
            print("No view metadata available, using all DICOMs")

        return dicoms
        
    def _extract_dicoms(self, request: PredictRequest) -> list:
        """Extract and filter multi-frame DICOMs from the request payload."""
        dicoms = []
        total = 0
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                total += 1
                instance_data = request.seriesInstanceImages[series_number][instance_number]

                if isinstance(instance_data, dict):
                    dicom_base64 = instance_data.get("image", instance_data)
                else:
                    dicom_base64 = instance_data

                if isinstance(dicom_base64, str) and not self._is_valid_base64(dicom_base64):
                    print(f"Invalid base64 string for series {series_number} instance {instance_number}")
                    continue

                dicom_data = base64.b64decode(dicom_base64)
                if not self._is_valid_dicom(dicom_data):
                    print(f"Invalid DICOM data for series {series_number} instance {instance_number}")
                    continue

                dicom = pydicom.dcmread(BytesIO(dicom_data))

                # Skip single-frame DICOMs — PanEcho expects video clips
                num_frames = getattr(dicom, 'NumberOfFrames', None)
                if num_frames is None or int(num_frames) <= 1:
                    print(f"Skipping single-frame DICOM for series {series_number} instance {instance_number}")
                    continue

                dicoms.append(dicom)

        print(f"{total} DICOMs found, {len(dicoms)} multi-frame DICOMs kept")
        return dicoms

    async def _handle_html_output(self, request: PredictRequest):
        try:
            dicoms = self._extract_dicoms(request)
            dicoms = self._prepare_dicoms_for_inference(dicoms, request.additionalMetadata)
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
        
        # The API schema (`JsonPredictionResponse`) expects the *diagnosis* field
        # to be a **string**.  We therefore serialise the dictionary into a JSON
        # string so that downstream consumers still get a single text field
        # while retaining full information.
        diagnosis = json.dumps(diagnosis_dict)
        
        # Generate recommendations based on stenosis analysis
        try:
            recommendations_en = self._get_recommendations(diagnosis_dict, "en")
            recommendations_fr = self._get_recommendations(diagnosis_dict, "fr")
        except Exception as e:
            print(f"Error in _get_recommendations: {e}")
            return {
                "diagnosis": "Error in _get_recommendations",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in _get_recommendations",
                    "fr": "Erreur dans _get_recommendations",
                    "presentable": True,
                }
            }
        
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
        print(f"request.additionalMetadata: {request.additionalMetadata}")

        try:
            dicoms = self._extract_dicoms(request)
            dicoms = self._prepare_dicoms_for_inference(dicoms, request.additionalMetadata)
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
        try:
            transform = self._create_transform(normalization=self.config.models['pan_echo'].normalization)
            device = 'cuda' if torch.cuda.is_available() else 'cpu'
            MAX_BATCH_SIZE = 8

            accumulated_logits: Dict[str, torch.Tensor] = {}
            total_clips = 0

            # Process in chunks — never more than MAX_BATCH_SIZE in memory
            for batch_start in range(0, len(dicoms), MAX_BATCH_SIZE):
                batch_dicoms = dicoms[batch_start:batch_start + MAX_BATCH_SIZE]
                batch_clips = []

                for i, dicom in enumerate(batch_dicoms):
                    pixel_array: np.ndarray = dicom.pixel_array
                    if pixel_array.ndim < 3:
                        print(f"Skipping DICOM {batch_start + i} with invalid dimensions: {pixel_array.shape}")
                        continue

                    clip = self._load_clip_from_dicom(
                        pixel_array,
                        clip_len=self.config.models['pan_echo'].clip_len
                    )
                    clip_tensor = tv_tensors.Video(np.transpose(clip, (0, 3, 1, 2)))
                    clip_tensor = transform(clip_tensor)
                    clip_tensor = torch.permute(clip_tensor, (1, 0, 2, 3))
                    batch_clips.append(clip_tensor)

                if not batch_clips:
                    continue

                batch_tensor = torch.stack(batch_clips).to(device)

                with torch.no_grad():
                    batch_outputs = CustomPredictionService.models['pan_echo'](batch_tensor)

                for key, output_tensor in batch_outputs.items():
                    batch_sum = torch.sum(output_tensor, dim=0)
                    if key in accumulated_logits:
                        accumulated_logits[key] += batch_sum
                    else:
                        accumulated_logits[key] = batch_sum.clone()

                total_clips += len(batch_clips)

                # Free GPU memory between batches
                del batch_tensor, batch_outputs
                torch.cuda.empty_cache()

            if total_clips == 0:
                raise ValueError("No valid DICOM files processed")

            averaged_logits = {
                key: logits / total_clips
                for key, logits in accumulated_logits.items()
            }

            return averaged_logits
        
        except Exception as e:
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            print(f"Error in _run_inference_logits_only: {e}")
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
