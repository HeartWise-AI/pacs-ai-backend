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
        print(f"predictions.keys(): {predictions.keys()}")
        
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
        
        def get_artery_status(artery_data):
            """Extract blocked, CTO, and thrombus status from artery data"""
            status = []
            if artery_data.get('diagnosis_stenosis') == 'blocked':
                status.append('stenosis')
                status.append(f"{artery_data.get('stenosis_prob')*100:.1f}%")       
            if artery_data.get('diagnosis_cto') == 'cto':
                status.append('cto')
                status.append(f"{artery_data.get('cto_prob')*100:.1f}%")
            if artery_data.get('diagnosis_thrombus') == 'thrombus':
                status.append('thrombus')
                status.append(f"{artery_data.get('thrombus_prob')*100:.1f}%")
            return status
        
        def format_artery_list(arteries_dict, system_name):
            """Format arteries with their conditions for a specific system"""
            affected_arteries = []
            
            for artery_name, data in arteries_dict.items():
                status = get_artery_status(data)
                if status:
                    # Get artery name
                    display_name = artery_names[system_name].get(artery_name, artery_name)
                    status_text = ', '.join(status)
                    affected_arteries.append(f"{display_name} ({status_text})")
            
            if not affected_arteries:
                return None
                
            return f"{system_name}: {', '.join(affected_arteries)}"
        
        # Generate paragraphs for each system
        paragraphs = []
        
        # RCA System
        rca_paragraph = format_artery_list(rca_arteries, 'Right Coronary Artery (RCA) System')
        if rca_paragraph:
            paragraphs.append(rca_paragraph)
        
        # LCA System  
        lca_paragraph = format_artery_list(lca_arteries, 'Left Coronary Artery (LCA) System')
        if lca_paragraph:
            paragraphs.append(lca_paragraph)
        
        # Other arteries
        other_paragraph = format_artery_list(other_arteries, 'Other')
        if other_paragraph:
            paragraphs.append(other_paragraph)
        
        if not paragraphs:
            return "No significant coronary pathology detected."
        
        # Join paragraphs
        return "Detected pathologies:\n" + "\n".join(paragraphs)

    def _get_recommendations(self, predictions: dict, language: str = "en") -> str:
        """
        Generate recommendations based on predictions
        """
        class_mapping = CustomPredictionService._class_mapping

        # 2) Mapping for human-readable artery names
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
        # 3) Check which arteries are above threshold
        # ------------------------------------------------------------------
        blocked_arteries = []
        cto_arteries = []
        thrombus_arteries = []

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

        # ------------------------------------------------------------------
        # 4) Generate simplified clinical recommendations
        # ------------------------------------------------------------------
        recommendations = []
        
        # Count the most severe conditions for priority assessment
        has_stenosis = len(blocked_arteries) > 0
        has_cto = len(cto_arteries) > 0
        has_thrombus = len(thrombus_arteries) > 0
        
        if has_thrombus:
            # Thrombus is the highest priority - requires immediate attention
            if language == "fr":
                recommendations.append(
                    "Thrombus coronarien détecté. ÉVALUATION URGENTE REQUISE. "
                    "Consulter immédiatement un cardiologue interventionnel pour "
                    "évaluation de thrombolyse ou thrombectomie selon la situation clinique."
                )
            else:
                recommendations.append(
                    "Coronary thrombus detected. URGENT EVALUATION REQUIRED. "
                    "Immediately consult an interventional cardiologist for "
                    "thrombolysis or thrombectomy evaluation based on clinical situation."
                )
        
        elif has_cto:
            # CTO requires specialized intervention
            if language == "fr":
                recommendations.append(
                    "Occlusion chronique totale (CTO) détectée. "
                    "Évaluation spécialisée recommandée pour techniques de revascularisation "
                    "avancées (rétrograde, dissection subintimale)."
                )
            else:
                recommendations.append(
                    "Chronic Total Occlusion (CTO) detected. "
                    "Specialized evaluation recommended for advanced revascularization "
                    "techniques (retrograde, subintimal dissection)."
                )
        
        elif has_stenosis:
            # Stenosis requires standard PCI evaluation
            if language == "fr":
                recommendations.append(
                    "Sténose coronarienne détectée. "
                    "Consulter un cardiologue interventionnel pour "
                    "évaluation d'une intervention coronarienne percutanée (ICP)."
                )
            else:
                recommendations.append(
                    "Coronary stenosis detected. "
                    "Consult an interventional cardiologist for "
                    "percutaneous coronary intervention (PCI) evaluation."
                )
        
        else:
            # No significant pathology detected
            if language == "fr":
                recommendations.append(
                    "Aucune pathologie coronarienne significative détectée. "
                    "Continuer la surveillance clinique de routine selon les protocoles établis."
                )
            else:
                recommendations.append(
                    "No significant coronary pathology detected. "
                    "Continue routine clinical monitoring per established protocols."
                )
        
        if not recommendations:
            if language == "fr":
                return (
                    "Aucune pathologie coronarienne significative détectée. "
                    "Continuer la surveillance clinique de routine selon les protocoles établis."
                )
            else:
                return (
                    "No significant coronary pathology detected. "
                    "Continue routine clinical monitoring per established protocols."
                )
        
        return "\n\n".join(recommendations)
        

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = []
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                dicoms.append(pydicom.dcmread(BytesIO(base64.b64decode(dicom_base64))))

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

        # The API schema (`JsonPredictionResponse`) expects the *diagnosis* field
        # to be a **string**.  We therefore serialise the dictionary into a JSON
        # string so that downstream consumers still get a single text field
        # while retaining full information.
        try:    
            structured_predictions_json = json.dumps(structured_predictions)
        except Exception as e:
            print(f"Error in json.dumps(structured_predictions): {e}")
            structured_predictions_json = "Error in json.dumps(structured_predictions)"

        # Generate recommendations based on stenosis analysis
        recommendations_en = self._get_recommendations(structured_predictions, "en")
        recommendations_fr = self._get_recommendations(structured_predictions, "fr")
        
        # Prepare comprehensive data for HTML parser
        html_data = {
            "diagnosis": diagnosis,
            "probability": structured_predictions,
            "recommendations": {"en": recommendations_en, "fr": recommendations_fr},
        }

        html_output = HTMLParser.generate_detection_results(html_data)
        return {"htmlBase64": base64.b64encode(html_output.encode("utf-8")).decode("utf-8")}

    async def _handle_json_output(self, request: PredictRequest):
        dicoms = []
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                dicoms.append(pydicom.dcmread(BytesIO(base64.b64decode(dicom_base64))))
                
        probability = self._run_inference(dicoms)

        if not probability:
            return {
                "diagnosis": "No video could be extracted or processed from the current DICOM series",
                "predictions": {},
                "modelRecommendations": {
                    "en": "No video could be extracted or processed from the current DICOM series",
                    "fr": "Aucune vidéo ne peut être extraite ou traitée à partir de la série DICOM actuelle",
                    "presentable": True,
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
        
        # The API schema (`JsonPredictionResponse`) expects the *diagnosis* field
        # to be a **string**.  We therefore serialise the dictionary into a JSON
        # string so that downstream consumers still get a single text field
        # while retaining full information.
        try:    
            structured_predictions_json = json.dumps(structured_predictions)
        except Exception as e:
            print(f"Error in json.dumps(structured_predictions): {e}")
            structured_predictions_json = "Error in json.dumps(structured_predictions)"

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
            "predictions": structured_predictions_json,
            "modelRecommendations": {
                "en": recommendations_en,
                "fr": recommendations_fr,
                "presentable": True,
            },
        }

    def videoShenanigans(self, video):
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

    def _run_inference(self, dicoms: list[pydicom.Dataset]) -> dict[str, float] | None:
        try:
            videos = []
            max_videos = CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"]
            
            print(f"nb_dicoms: {len(dicoms)} received, max_videos: {max_videos} expected")
            
            stop_pt = min(len(dicoms), max_videos)
            dicom_ok = 0
            for dicom in dicoms:
                if dicom_ok >= stop_pt:
                    break

                pixel_array: np.ndarray = dicom.pixel_array

                if pixel_array.ndim == 1 or pixel_array.ndim == 2:
                    continue

                if pixel_array.ndim == 3:
                    # Expand single channel to 3 channels by repeating
                    pixel_array = np.expand_dims(pixel_array, axis=1)  # Shape: F,1,W,H
                    pixel_array = np.repeat(pixel_array, 3, axis=1)  # Shape: F,3,W,H

                assert pixel_array.ndim == 4, "Pixel array must have 4 dimensions"

                pixel_array = self.videoShenanigans(pixel_array.transpose(0, 2, 3, 1)).astype(
                    np.uint8
                )
                pixel_array = pixel_array.astype(np.float32)

                mean = [112.24039459228516, 112.24039459228516, 112.24039459228516]
                std = [39.012229919433594, 39.012229919433594, 39.012229919433594]

                # Convert numpy array to torch tensor and ensure float type
                video = torch.from_numpy(pixel_array)
                video = v2.Resize((224, 224), antialias=None)(video)
                video = v2.Normalize(mean, std)(video)
                video = video.permute(1, 0, 2, 3)
                video = video.numpy()

                c, f, h, w = video.shape
                length = 16
                if f < length * 2:
                    video = np.concatenate(
                        (video, np.zeros((c, length * 2 - f, h, w), video.dtype)), axis=1
                    )
                    c, f, h, w = video.shape
                start = np.array([0])
                video = tuple(video[:, s + 2 * np.arange(length), :, :] for s in start)[0]
                videos.append(video)
                dicom_ok += 1

            video_batch = torch.from_numpy(np.array(videos))
            video_batch = video_batch.permute(0, 2, 3, 4, 1)
            
            # Zero pad the video_batch if we have fewer videos than max_videos
            if video_batch.shape[0] < max_videos:
                print(f"Padding video_batch from {video_batch.shape[0]} to {max_videos} videos")
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

            with torch.no_grad():
                output: torch.Tensor = CustomPredictionService.models["video_mil_wrapper"](
                    video_batch
                )

            # Normalize the output
            for key in output:
                if "binary" in key or "cto" in key or "thrombus" in key:  # Compute probability output
                    output[key] = torch.sigmoid(output[key])
                else:  # Clamp output to 0-100
                    output[key] = torch.clamp(output[key], min=0, max=100)

            return output

        except Exception:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            
            return None
