import base64
import json
import os
import pprint
import uuid
from io import BytesIO

import cv2 as cv
import numpy as np
import pydicom
import torch
from models.multi_instance_linear_probing import MultiInstanceLinearProbing
from models.video_encoder import VideoEncoder
from torchvision.transforms import v2
from utils.genericLogic import BasePredictionService
from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest
from pathlib import Path
from typing import Optional, Tuple


class VideoMILWrapper(torch.nn.Module):
    def __init__(self, video_encoder, mil_model, num_videos: int):
        """Wrapper around *VideoEncoder* and *MultiInstanceLinearProbing*.

        Args
        ----
        video_encoder: Backbone that outputs either per-video embeddings
            ``[B, N, D]`` or per-patch embeddings ``[B, N_tokens, D]`` where
            tokens are ordered consecutively for each video.
        mil_model:  Multi-Instance head (e.g. *MultiInstanceLinearProbing*).
        num_videos: Expected number of videos/segments per sample (*N*).
            This is needed to reshape flat per-patch tokens into
            ``[B, N, L, D]`` so that the downstream MIL model can perform
            hierarchical pooling.
        """
        super().__init__()
        self.video_encoder = video_encoder
        self.mil_model = mil_model
        self.num_videos: int = num_videos

    def forward(
        self, x: torch.Tensor, video_indices: torch.Tensor | None = None
    ) -> dict[str, torch.Tensor]:
        """Wrapper forward pass.

        This helper guarantees that the **MultiInstanceLinearProbing** module
        always receives a 3-D tensor of shape ``[B, N, D]`` where *N* is the
        number of video segments associated with each sample.

        In the typical *single-video* case, ``video_indices`` will be ``None``
        and the underlying ``VideoEncoder`` already returns a tensor of shape
        ``[B, D]`` (aggregated representation).  We therefore unsqueeze a
        singleton *N* dimension so that it becomes ``[B, 1, D]``.

        When ``multi_video=True`` the dataloader supplies inputs of shape
        ``[B, N, C, F, H, W]``.  If the encoder is configured with
        ``aggregate_videos_tokens=False`` it will naturally produce the
        expected ``[B, N, D]`` tensor.  However, if
        ``aggregate_videos_tokens=True`` the encoder will *already* pool over
        the *N* dimension and output ``[B, D]``.  In this scenario we again
        expand the aggregated vector so that downstream MIL logic continues
        to work (with ``N = 1``).
        """

        # ------------------------------------------------------------------
        # 1) Run the backbone / encoder
        # ------------------------------------------------------------------
        embeddings: torch.Tensor = self.video_encoder(x)

        # ------------------------------------------------------------------
        # 2) Reshape so that the MIL module always sees *either*:
        #    • 3-D tensor  [B, N, D]  (per-video embeddings)
        #    • 4-D tensor  [B, N, L, D] (per-patch tokens, hierarchical)
        # ------------------------------------------------------------------

        if embeddings.ndim == 2:
            # Encoder returned [B, D] → add singleton video dimension
            embeddings = embeddings.unsqueeze(1)  # [B, 1, D]

        elif embeddings.ndim == 3 and embeddings.shape[1] > self.num_videos:
            # print(f"Received flat patch tokens [B, N*L, D]. Reshaping into [B, N, L, D]")
            # Received flat patch tokens [B, N*L, D].  Reshape into
            # hierarchical layout [B, N, L, D] so that the MIL head can
            # perform two-level attention.
            B, NL, D = embeddings.shape  # noqa: N806
            # print(f"embeddings.shape: {embeddings.shape}")
            if NL % self.num_videos != 0:
                raise ValueError(
                    f"Number of tokens (NL={NL}) is not divisible by the "
                    f"expected num_videos={self.num_videos}. Cannot "
                    "infer tokens per video for hierarchical pooling."
                )
            L = NL // self.num_videos
            embeddings = embeddings.view(B, self.num_videos, L, D)  # [B,N,L,D]

        # ------------------------------------------------------------------
        # 3) Build attention mask (video-level only for now)
        # ------------------------------------------------------------------
        if embeddings.ndim == 4:
            B, N, _, _ = embeddings.shape
        else:
            B, N, _ = embeddings.shape  # type: ignore[misc]

        # Build a simple boolean mask that marks every video as valid.  In the
        # future we could incorporate ``video_indices`` to create selective
        # masks, e.g. when some videos are padded.
        attention_mask = torch.ones((B, N), dtype=torch.bool, device=embeddings.device)

        # ------------------------------------------------------------------
        # 4) Forward through the MIL head(s)
        # ------------------------------------------------------------------
        return self.mil_model(embeddings, mask=attention_mask)


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        print("Loading model")

        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return

        # Load the class mapping from the local package
        class_mapping_path = os.path.join("models", "class_mapping.json")
        with open(class_mapping_path) as fp:
            CustomPredictionService._class_mapping = json.load(fp)

        # Load the HuggingFacemodel config 
        with open(os.path.join("models", "config.json")) as fp:
            CustomPredictionService.model_config = json.load(fp)
            
        # Create and load the model
        print(f"Model path: {CustomPredictionService.model_config["ModelStateDict"]["model_path"]}")
        try:
            print("Loading video encoder")
            video_encoder = VideoEncoder(
                **CustomPredictionService.model_config["VideoEncoder"],
            )
            
            print("Loading multi-instance linear probing")
            head_structure = {head: value["head_dim"] for head, value in CustomPredictionService._class_mapping.items()}        
            mil_model = MultiInstanceLinearProbing(
                **CustomPredictionService.model_config["MultiInstanceLinearProbing"],
                head_structure=head_structure,
            )
            print("Creating video MIL wrapper")
            CustomPredictionService.models["video_mil_wrapper"] = VideoMILWrapper(
                video_encoder, mil_model, num_videos=CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"]
            )
            print("Successfully created video MIL wrapper model directly from local package")
            
        except Exception as e2:
            # If both methods fail, raise a clear error
            raise RuntimeError(
                f"Cannot initialize model: second attempt error: {str(e2)}"
            ) from e2

        # Load the model state dict
        model_state_dict = torch.load(
            os.path.join("models", CustomPredictionService.model_config["ModelStateDict"]["model_path"]),
            map_location=torch.device("cpu"),
            weights_only=True,
        )["linear_probing"]
        # Remove the "module." prefix from the keys
        model_state_dict = {k.replace("module.", ""): v for k, v in model_state_dict.items()}

        # Load the model state dict into the video MIL wrapper
        CustomPredictionService.models["video_mil_wrapper"].load_state_dict(model_state_dict)
        CustomPredictionService.models["video_mil_wrapper"].eval()

        # Move the model to the GPU if available
        CustomPredictionService.models["video_mil_wrapper"].to(
            "cuda" if torch.cuda.is_available() else "cpu"
        )
        CustomPredictionService.is_initialized = True
        print(f"Cuda available: {torch.cuda.is_available()}")
        print("Model loaded")

    def _process_predictions(
        self, 
        predictions: dict[str, float | torch.Tensor]
    ) -> dict[str, dict]:
        """Process and structure raw model predictions into a structured format.

        This function transforms raw model outputs by:
        1. Mapping model keys to human-readable artery names using class_mapping.json
        2. Converting tensor values to Python floats
        3. Applying thresholds to determine binary classifications (normal vs blocked/cto/thrombus/calcified)
        4. Structuring the output into a nested dictionary format organized by artery

        Args:
            predictions: Dictionary of raw model predictions with keys like 
                       'leftmain_stenosis_binary', 'lad_stenosis_regression', etc.

        Returns:
            Nested dictionary with structure:
            {
                "artery_name": {
                    "stenosis_prob": float,           # For binary stenosis heads
                    "diagnosis_stenosis": str,        # 'normal' or 'blocked'
                    "regression": float,              # For regression heads (0-100%)
                    "cto_prob": float,                # For CTO heads
                    "diagnosis_cto": str,             # 'normal' or 'cto'
                    "thrombus_prob": float,           # For thrombus heads
                    "diagnosis_thrombus": str,        # 'normal' or 'thrombus'
                    "calcif_prob": float,             # For calcification heads
                    "diagnosis_calcif": str           # 'normal' or 'calcified'
                }
            }
        """
        class_mapping = CustomPredictionService._class_mapping
        
        reordered_predictions = {}
        for key in predictions.keys():
            new_key = class_mapping[key]['name']

            if not new_key in reordered_predictions:
                reordered_predictions[new_key] = {}
            
            if 'stenosis_binary' in key:
                reordered_predictions[new_key]['stenosis_prob'] = predictions[key].item()
                reordered_predictions[new_key]['diagnosis_stenosis'] = 'blocked' if predictions[key].item() > class_mapping[key]['threshold'] else 'normal'
            elif 'stenosis' in key and not 'binary' in key:
                reordered_predictions[new_key]['regression'] = predictions[key].item()
            elif 'cto' in key:
                reordered_predictions[new_key]['cto_prob'] = predictions[key].item()
                reordered_predictions[new_key]['diagnosis_cto'] = 'cto' if predictions[key].item() > class_mapping[key]['threshold'] else 'normal'
            elif 'thrombus' in key:
                reordered_predictions[new_key]['thrombus_prob'] = predictions[key].item()
                reordered_predictions[new_key]['diagnosis_thrombus'] = 'thrombus' if predictions[key].item() > class_mapping[key]['threshold'] else 'normal'
            elif 'calcif' in key:
                reordered_predictions[new_key]['calcif_prob'] = predictions[key].item()
                reordered_predictions[new_key]['diagnosis_calcif'] = 'calcified' if predictions[key].item() > class_mapping[key]['threshold'] else 'normal'
            else:
                raise ValueError(f"Unknown key: {key}")
            
        return reordered_predictions

    def _get_diagnosis(self, predictions: dict) -> str:
        """
        Generate diagnosis string in English
        """
        artery_names: dict[str, dict[str, str]] = {
            'Right Coronary Artery (RCA) System': {
                'Proximal RCA': 'Proximal RCA',
                'Mid RCA': 'Mid RCA',
                'Distal RCA': 'Distal RCA',
                'Posterior Descending Artery': 'Posterior Descending Artery',
                'Posterolateral Branch': 'Posterolateral Branch'
            },
            'Left Coronary Artery (LCA) System': {
                'Left Main Branch': 'Left Main Branch',
                'Proximal LAD': 'Proximal LAD',
                'Mid LAD': 'Mid LAD',
                'Distal LAD': 'Distal LAD',
                'D1 Branch': 'D1 Branch',
                'D2 Branch': 'D2 Branch',
                'Proximal LCX': 'Proximal LCX',
                'Distal LCX': 'Distal LCX',
                'Mid LCX': 'Mid LCX',
                'OM1 (Obtuse Marginal 1)': 'OM1 (Obtuse Marginal 1)',
                'OM2 (Obtuse Marginal 2)': 'OM2 (Obtuse Marginal 2)',
            },
            'Other': {
                'Branch Vessel': 'Branch Vessel',
                'LVp': 'LVp'
            }
        }
        
        # Classify arteries by system
        rca_arteries = {}
        lca_arteries = {}
        other_arteries = {}
        
        for artery_name, data in predictions.items():
            if artery_name in artery_names['Right Coronary Artery (RCA) System']:
                rca_arteries[artery_name] = data
            elif artery_name in artery_names['Left Coronary Artery (LCA) System']:
                lca_arteries[artery_name] = data
            elif artery_name in artery_names['Other']:
                other_arteries[artery_name] = data
        
        def format_artery_diagnosis(artery_name, data):
            """Format a single artery's diagnosis with all conditions."""
            parts = []
            
            # Stenosis
            stenosis_status = data.get('diagnosis_stenosis')
            if stenosis_status == 'blocked':
                regression = data.get('regression')
                parts.append(f"stenosis: blocked ({regression:.1f}%)")
            else:
                parts.append(f"stenosis: {stenosis_status}")
            
            # Calcification
            parts.append(f"calcified: {data.get('diagnosis_calcif', 'normal')}")
            
            # CTO
            parts.append(f"cto: {data.get('diagnosis_cto', 'normal')}")
            
            # Thrombus
            parts.append(f"thrombus: {data.get('diagnosis_thrombus', 'normal')}")
            
            return f"  {artery_name} - {', '.join(parts)}"
        
        paragraphs = []
        
        # RCA paragraph
        rca_lines = ["RCA:"]
        for artery_name, data in rca_arteries.items():
            rca_lines.append(format_artery_diagnosis(artery_name, data))
        paragraphs.append("\n".join(rca_lines))
        
        # LCA paragraph
        lca_lines = ["LCA:"]
        for artery_name, data in lca_arteries.items():
            lca_lines.append(format_artery_diagnosis(artery_name, data))
        paragraphs.append("\n".join(lca_lines))
        
        # Other paragraph
        other_lines = ["Other:"]
        for artery_name, data in other_arteries.items():
            other_lines.append(format_artery_diagnosis(artery_name, data))
        paragraphs.append("\n".join(other_lines))
        
        return "Model diagnosis:\n" + "\n".join(paragraphs)

    def _get_recommendations(self, predictions: dict, language: str = "en") -> str:
        """
        Generate recommendations based on predictions
        """

        # 1) Mapping for human-readable artery names
        # ------------------------------------------------------------------
        artery_names: dict[str, dict[str, str]] = {
            'Right Coronary Artery (RCA) System': {
                'fr': 'Tronc Coronarien Droit (TCD)',
                'Proximal RCA': 'CD Proximale',
                'Mid RCA': 'CD Moyenne',
                'Distal RCA': 'CD Distale',
                'Posterior Descending Artery': 'IVP Postérieure',
                'Posterolateral Branch': 'Branche Postérolatérale'
            },
            'Left Coronary Artery (LCA) System': {
                'fr': 'Tronc Coronarien Gauche (TCG)',
                'Left Main Branch': 'Tronc Commun Gauche',
                'Proximal LAD': 'IVA Proximale',
                'Mid LAD': 'IVA Moyenne',
                'Distal LAD': 'IVA Distale',
                'D1 Branch': 'Branche D1',
                'D2 Branch': 'Branche D2',
                'Proximal LCX': 'Circonflexe Gauche Proximal',
                'Distal LCX': 'Circonflexe Gauche Distal',
                'Mid LCX': 'Circonflexe Gauche Moyenne',
                'OM1 (Obtuse Marginal 1)': 'MO1 Marginale Obtuse 1',
                'OM2 (Obtuse Marginal 2)': 'MO2 Marginale Obtuse 2',
            },
            'Other': {
                'fr': 'Autre',
                'Branch Vessel': 'Branche Vasculaire',
                'LVp': 'LVp'
            }
        }
        
        # ------------------------------------------------------------------
        # 2) Check which arteries are above threshold
        # ------------------------------------------------------------------
        blocked_arteries = []
        cto_arteries = []
        thrombus_arteries = []
        calcification_arteries = []
        for artery_name, data in predictions.items():
            # Check for blocked arteries (stenosis)
            if data.get('diagnosis_stenosis') == 'blocked':
                prob_value = data.get('stenosis_prob', 0)
                if isinstance(prob_value, torch.Tensor):
                    prob_value = prob_value.item()
                percentage = prob_value * 100
                localized_name = None
                
                # Find the artery in our mapping
                for system_name, arteries in artery_names.items():
                    if artery_name in arteries:
                        localized_name = arteries.get(artery_name, artery_name)
                        break
                
                if localized_name:
                    blocked_arteries.append((localized_name, percentage))
            
            # Check for CTO
            if data.get('diagnosis_cto') == 'cto':
                prob_value = data.get('cto_prob', 0)
                if isinstance(prob_value, torch.Tensor):
                    prob_value = prob_value.item()
                percentage = prob_value * 100
                localized_name = None
                
                for system_name, arteries in artery_names.items():
                    if artery_name in arteries:
                        localized_name = arteries.get(artery_name, artery_name)
                        break
                
                if localized_name:
                    cto_arteries.append((localized_name, percentage))
            
            # Check for thrombus
            if data.get('diagnosis_thrombus') == 'thrombus':
                prob_value = data.get('thrombus_prob', 0)
                if isinstance(prob_value, torch.Tensor):
                    prob_value = prob_value.item()
                percentage = prob_value * 100
                localized_name = None
                
                for system_name, arteries in artery_names.items():
                    if artery_name in arteries:
                        localized_name = arteries.get(artery_name, artery_name)
                        break
                
                if localized_name:
                    thrombus_arteries.append((localized_name, percentage))

            # Check for calcification
            if data.get('diagnosis_calcif') == 'calcified':
                prob_value = data.get('calcif_prob', 0)
                if isinstance(prob_value, torch.Tensor):
                    prob_value = prob_value.item()
                percentage = prob_value * 100
                localized_name = None
                
                for system_name, arteries in artery_names.items():
                    if artery_name in arteries:
                        localized_name = arteries.get(artery_name, artery_name)
                        break
                if localized_name:
                    calcification_arteries.append((localized_name, percentage))

        # ------------------------------------------------------------------
        # 3) Generate simplified clinical recommendations
        # ------------------------------------------------------------------
        recommendations = []
        
        # Count the most severe conditions for priority assessment
        has_stenosis = len(blocked_arteries) > 0
        has_cto = len(cto_arteries) > 0
        has_thrombus = len(thrombus_arteries) > 0
        has_calcification = len(calcification_arteries) > 0
        
        if has_thrombus:
            # Thrombus is the highest priority - requires immediate attention
            if language == "fr":
                recommendations.append(
                    "<strong>Thrombus coronarien détecté. ÉVALUATION URGENTE REQUISE.</strong> "
                    "Consulter immédiatement un cardiologue interventionnel pour "
                    "évaluation de thrombolyse ou thrombectomie selon la situation clinique. <br><br>"
                )
            else:
                recommendations.append(
                    "<strong>Coronary thrombus detected. URGENT EVALUATION REQUIRED.</strong> "
                    "Immediately consult an interventional cardiologist for "
                    "thrombolysis or thrombectomy evaluation based on clinical situation. <br><br>"
                )
        
        if has_cto:
            # CTO requires specialized intervention
            if language == "fr":
                recommendations.append(
                    "<strong>Occlusion chronique totale (CTO) détectée.</strong> "
                    "Évaluation spécialisée recommandée pour techniques de revascularisation "
                    "avancées (rétrograde, dissection subintimale). <br><br>"
                )
            else:
                recommendations.append(
                    "<strong>Chronic Total Occlusion (CTO) detected.</strong> "
                    "Specialized evaluation recommended for advanced revascularization "
                    "techniques (retrograde, subintimal dissection). <br><br>"
                )
        
        if has_stenosis:
            # Stenosis requires standard PCI evaluation
            if language == "fr":
                recommendations.append(
                    "<strong>Sténose coronarienne détectée.</strong> "
                    "Consulter un cardiologue interventionnel pour "
                    "évaluation d'une intervention coronarienne percutanée (ICP). <br><br>"
                )
            else:
                recommendations.append(
                    "<strong>Coronary stenosis detected.</strong> "
                    "Consult an interventional cardiologist for "
                    "percutaneous coronary intervention (PCI) evaluation. <br><br>"
                )
        if has_calcification:
            if language == "fr":
                recommendations.append(
                    "<strong>Calcification coronarienne détectée.</strong> "
                    "Consulter un cardiologue interventionnel pour "
                    "évaluation d'une intervention coronarienne percutanée (ICP). <br><br>"
                )
            else:
                recommendations.append(
                    "<strong>Coronary calcification detected.</strong> "
                    "Consult an interventional cardiologist for "
                    "percutaneous coronary intervention (PCI) evaluation. <br><br>"
                )
                
        if not recommendations:
            if language == "fr":
                return (
                    "<strong>Aucune pathologie coronarienne significative détectée.</strong> "
                    "Continuer la surveillance clinique de routine selon les protocoles établis. <br><br>"
                )
            else:
                return (
                    "<strong>No significant coronary pathology detected.</strong> "
                    "Continue routine clinical monitoring per established protocols. <br><br>"
                )
        
        return "\n\n".join(recommendations)
        
    def _filter_dicoms_with_metadata(self, dicoms: list[pydicom.Dataset], metadata: dict) -> list[pydicom.Dataset]:
        """
        Filter DICOMs to keep only Left/Right Coronary with diagnostic status.
        """
        allowed_views = ['Left Coronary', 'Right Coronary']
        filtered_dicoms = []
        for dicom in dicoms:
            dicom_name = str(dicom.SeriesInstanceUID)
            if dicom_name not in metadata:
                continue
            dicom_meta = metadata[dicom_name]
            if dicom_meta.get('main_structure') not in allowed_views:
                continue
            if dicom_meta.get('status') != 'diagnostic':
                continue
            filtered_dicoms.append(dicom)
        return filtered_dicoms

    async def _handle_html_output(self, request: PredictRequest):        
        dicoms = []
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                dicoms.append(pydicom.dcmread(BytesIO(base64.b64decode(dicom_base64))))

        if request.seriesInstanceMetadata:
            filtered_dicoms = self._filter_dicoms_with_metadata(
                dicoms, 
                request.seriesInstanceMetadata
            )
            print(f"Filtering: {len(dicoms)} total DICOMs, {len(filtered_dicoms)} matched (Left/Right Coronary + diagnostic)")
            if filtered_dicoms:
                dicoms = filtered_dicoms
                print(f"Using {len(dicoms)} filtered DICOMs")
            else:
                print(f"No matches, using all {len(dicoms)} DICOMs")
        else:
            print("No series instance metadata, using all DICOMs")
            
        probability = self._run_inference(dicoms)

        if not probability:
            return {
                "htmlBase64": base64.b64encode(b"<h1>No video could be extracted or processed from the current DICOM series</h1>").decode("utf-8"),
                "diagnosis": "No video could be extracted or processed from the current DICOM series",
                "probability": {},
                "recommendations": {
                    "en": "No video could be extracted or processed from the current DICOM series",
                    "fr": "Aucune vidéo ne peut être extraite ou traitée à partir de la série DICOM actuelle"
                }
            }
            
        # Obtain per-head diagnosis/interpretation
        structured_predictions: dict[str, dict] = self._process_predictions(probability)

        # Transform into a diagnosis string
        try:    
            diagnosis = self._get_diagnosis(structured_predictions)
        except Exception as e:
            print(f"Error in _get_diagnosis: {e}")
            diagnosis = "Error in _get_diagnosis"

        # Generate recommendations based on stenosis analysis
        recommendations_en = self._get_recommendations(structured_predictions, "en")
        recommendations_fr = self._get_recommendations(structured_predictions, "fr")
        
        # Prepare comprehensive data for HTML parser
        html_data = {
            "diagnosis": diagnosis,
            "probability": structured_predictions,
            "recommendations": {"en": recommendations_en, "fr": recommendations_fr},
        }

        try:
            html_output = HTMLParser.generate_detection_results(html_data)
        except Exception as e:
            print(f"Error in HTMLParser.generate_detection_results: {e}")
            html_output = "Error in HTMLParser.generate_detection_results"

        return {"htmlBase64": base64.b64encode(html_output.encode("utf-8")).decode("utf-8")}

    async def _handle_json_output(self, request: PredictRequest):
        dicoms = []
        
        print(f"request.seriesInstanceMetadata: {request.seriesInstanceMetadata}")

        try:
            for series_number in request.seriesInstanceImages:                
                for instance_number in request.seriesInstanceImages[series_number]:
                    try:
                        dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                        
                        # Validate base64 string
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
                
        if request.seriesInstanceMetadata:
            filtered_dicoms = self._filter_dicoms_with_metadata(
                dicoms,
                request.seriesInstanceMetadata
            )
            print(f"Filtering: {len(dicoms)} total DICOMs, {len(filtered_dicoms)} matched (Left/Right Coronary + diagnostic)")
            if filtered_dicoms:
                dicoms = filtered_dicoms
                print(f"Using {len(dicoms)} filtered DICOMs")
            else:
                print(f"No matches, using all {len(dicoms)} DICOMs")
        else:
            print("No series instance metadata, using all DICOMs")
                
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

        # Obtain per-head diagnosis/interpretation
        try:
            structured_predictions: dict[str, dict] = self._process_predictions(probability)
        except Exception as e:
            print(f"Error in _process_predictions: {e}")
            structured_predictions = {
                "diagnosis": "Error in _process_predictions",
                "predictions": "Error in _process_predictions",
                "modelRecommendations": {
                    "en": "Error in _process_predictions",
                    "fr": "Erreur dans _process_predictions",
                    "presentable": True,
                }
            }
            return structured_predictions

        
        # Transform into a diagnosis string
        try:    
            diagnosis = self._get_diagnosis(structured_predictions)
        except Exception as e:
            print(f"Error in _get_diagnosis: {e}")
            diagnosis = "Error in _get_diagnosis"
        
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
            "predictions": structured_predictions,
            "modelRecommendations": {
                "en": recommendations_en,
                "fr": recommendations_fr,
                "presentable": True,
            },
        }

    def process_dicom_to_video(
        self, 
        dicom: pydicom.Dataset, 
        dicom_name: str = None
    ) -> Optional[np.ndarray]:
        """
        Process a DICOM dataset to extract video using the process_dicom_video function.
        
        Args:
            dicom: DICOM dataset
            dicom_name: Optional name for the DICOM
            
        Returns:
            Processed video as numpy array or None if processing failed
        """
        try:
            # Create temporary file paths
            input_path = f"{dicom_name}.avi"

            # Process using the provided function
            avi_path = process_dicom_video(dicom, input_path)

            if avi_path is None:
                print(f"Failed to process DICOM {dicom_name}")
                return None
                
            # Read the processed AVI file
            frame_count = 0
            compressedVideo = []
            capture = cv.VideoCapture(avi_path)
            stride = CustomPredictionService.model_config["VideoMILWrapper"]["frame_stride"]
            try:
                while True:
                    ret, frame = capture.read()
                    if not ret:
                        break
                    if frame_count % stride == 0:
                        if frame.ndim == 3:
                            frame = cv.cvtColor(frame, cv.COLOR_BGR2RGB)
                        compressedVideo.append(frame)
                    frame_count += 1
            except Exception as e:
                print(f"Error in process_dicom_to_video: {e}")
                return None
            finally:
                capture.release()
                
            # Clean up temporary files
            try:
                if os.path.exists(input_path):
                    os.remove(input_path)
                if os.path.exists(avi_path):
                    os.remove(avi_path)
            except:
                pass
                
            return np.asarray(compressedVideo)
            
        except Exception as e:
            print(f"Error processing DICOM {dicom_name}: {e}")
            return None

    def _run_inference(self, dicoms: list[pydicom.Dataset]) -> dict[str, float] | None:
        try:
            videos = []
            max_videos = CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"]
                        
            stop_pt = min(len(dicoms), max_videos)
            dicom_ok = 0
            for dicom in dicoms:
                if dicom_ok >= stop_pt:
                    break

                # Extract DICOM name/identifier (still needed for logging and temp files)
                dicom_name = None
                try:
                    dicom_name = dicom.SeriesInstanceUID
                except:
                    dicom_name = f"temp_{uuid.uuid4()}"
                
                # Process video
                try:
                    video = self.process_dicom_to_video(dicom, dicom_name)
                    if video is None:
                        print(f"Failed to process DICOM {dicom_name}")
                        continue
                except Exception as e:
                    print(f"Error in process_dicom_to_video for {dicom_name}: {e}")
                    continue

                # Convert to float32 for normalization
                video = video.astype(np.float32)

                # Convert numpy array to torch tensor and ensure float type
                video = torch.from_numpy(video)
                               
                # Permute to [F,C,H,W] si besoin
                if video.shape[-1] in [1, 3]:
                    video = video.permute(0, 3, 1, 2)                
                
                t = video.shape[0]
                expected_frames = CustomPredictionService.model_config["VideoEncoder"]["num_frames"]
                # Gestion du nombre de frames
                if t < expected_frames:
                    last_frame = video[-1:].repeat(expected_frames - t, 1, 1, 1) # repeat the last frame to match the expected number of frames
                    video = torch.cat([video, last_frame], dim=0)
                elif t > expected_frames:
                    indices = torch.linspace(0, t - 1, expected_frames).long()
                    video = video[indices]     
                               
                # Resize the video
                video = v2.Resize((224, 224), antialias=True)(video)                            

                # Normalize the video
                mean = [105.24055480957031, 105.24055480957031, 105.24055480957031]
                std = [39.24827194213867, 39.24827194213867, 39.24827194213867]              
                video = v2.Normalize(mean, std)(video)
                
                # Permute to [F,C,H,W]
                video = video.permute(0, 2, 3, 1).contiguous()
            
                video = video.cpu().numpy()
                           
                videos.append(video)
                dicom_ok += 1

            video_batch = torch.from_numpy(np.array(videos)).to(dtype=torch.float16)
                        
            # Zero pad the video_batch if we have fewer videos than max_videos
            if video_batch.shape[0] < max_videos:
                # Get the shape of a single video (after permute)
                single_video_shape = video_batch.shape[1:]  # (C, H, W, T)
                # Create zero tensor for padding
                padding_shape = (max_videos - video_batch.shape[0],) + single_video_shape
                zero_padding = torch.zeros(padding_shape, dtype=video_batch.dtype, device=video_batch.device)
                # Concatenate the original videos with zero padding
                video_batch = torch.cat([video_batch, zero_padding], dim=0)
            
            video_batch = video_batch.unsqueeze(0).to(
                "cuda" if torch.cuda.is_available() else "cpu"
            )
            
            model = CustomPredictionService.models["video_mil_wrapper"]
            model.eval()
            
            with torch.no_grad():
                outputs: torch.Tensor = model(
                    video_batch
                )

            # Normalize the output
            for key in outputs:
                if "binary" in key or "cto" in key or "thrombus" in key:  # Compute probability output
                    outputs[key] = torch.sigmoid(outputs[key])
                else:  # Clamp output to 0-100
                    outputs[key] = torch.clamp(outputs[key], min=0, max=100)
            return outputs

        except Exception:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            
            return None
        
def process_dicom_video(
    dicom: pydicom.Dataset, 
    output_path: str
)->Optional[str]:
    """
    Converts DICOM videos to AVI format and extracts acquisition time information.
    
    Args:
        dicom (pydicom.Dataset): The DICOM dataset.
        output_path (str): The path to save the AVI file.
    
    Returns:
        Optional[str]: The path to the converted AVI file, or None if conversion failed.
    """
        
    # get pixel array
    video: np.ndarray = dicom.pixel_array
    
    # Insure extracted array is 3D
    if len(video.shape) != 3:
        print(f"Error: Extracted video's shape is not 3D: {video.shape} -  {dicom}") 
        return None
    
    # get frame height and width
    frame_height: int = dicom[(0x028, 0x0011)].value
    frame_width: int = dicom[(0x028, 0x0010)].value
    
    # Insure consistence between dicom info and extracted video
    if frame_height != video.shape[1]:
        print(f"Error: Dicom video height {frame_height} does not match extracted video's shape: {video.shape[1]} -  {dicom}") 
        return None
    
    if frame_width != video.shape[2]:
        print(f"Error: Dicom video width {frame_width} does not match extracted video's shape: {video.shape[2]} -  {dicom}") 
        return None
    
    # Extract FPS; ensure the DICOM tag exists
    fps: float = 30.0  # Default FPS if not specified
    if (0x08, 0x2144) in dicom:
        fps = float(dicom[(0x08, 0x2144)].value)
    
    try:
        photometrics: str = dicom.PhotometricInterpretation
        if photometrics not in ['MONOCHROME1', 'MONOCHROME2', 'RGB']:
            print(f"Error: Unsupported Photometric Interpretation: {photometrics} - with shape {video.shape}")
            return None
    except:
        print(f"Error in reading {dicom}")
        return None
        
    # Create video writer
    fourcc: int = cv.VideoWriter_fourcc("M", "J", "P", "G")
    out: cv.VideoWriter = cv.VideoWriter(output_path, fourcc, fps, (frame_width, frame_height))
        
    conversion_fn: int = cv.COLOR_GRAY2BGR if photometrics == 'MONOCHROME1' or photometrics == 'MONOCHROME2' else cv.COLOR_RGB2BGR    
    for frame in dicom.pixel_array:
        frame: np.ndarray = cv.cvtColor(frame, conversion_fn)
        out.write(frame)
    
    # Release video writer
    out.release()

    return output_path
