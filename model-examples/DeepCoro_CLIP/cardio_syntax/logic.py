import os
import json
import uuid
import torch
import base64
import pydicom

import cv2 as cv
import numpy as np

from io import BytesIO
from typing import Optional
from torchvision.transforms import v2

from utils.html_parser import HTMLParser
from models.video_encoder import VideoEncoder
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService
from models.multi_instance_linear_probing import MultiInstanceLinearProbing


# ---------------------------------------------------------------------------
# SYNTAX score thresholds (Global)
# Source: DeepCORO-CLIP SYNTAX paper (JACC-CV submission)
# "Global AI-SYNTAX thresholds were set based on prevalence in the training
#  set: ≤2.23 (No disease), 2.23–20.92 (Mild), 20.92–28.25 (Moderate),
#  >28.25 (Severe)"
# These are quantile-based thresholds derived from training-set prevalence,
# NOT the raw clinical SYNTAX cutoffs (22/32/33), because AI-predicted scores
# have a systematically different distribution to raw annotations.
# ---------------------------------------------------------------------------
SYNTAX_THRESHOLDS = {
    "no_disease":  2.23,   # ≤ 2.23
    "mild":       20.92,   # 2.23 – 20.92
    "moderate":   28.25,   # 20.92 – 28.25
    # severe:       > 28.25
}

SYNTAX_CATEGORY_LABELS = {
    "no_disease": "No Disease",
    "mild":       "Mild (Low SYNTAX)",
    "moderate":   "Moderate (Intermediate SYNTAX)",
    "severe":     "Severe (High SYNTAX)",
}

# Per-category clinical recommendations aligned with ESC/ACC revascularisation
# guidelines and the simplified treatment-triage framework described in the paper.
SYNTAX_RECOMMENDATIONS = {
    "no_disease": {
        "en": (
            "No significant coronary artery disease detected (AI-SYNTAX ≤ 2.23). "
            "No revascularization is indicated based on the estimated SYNTAX score."
        ),
        "fr": (
            "Aucune maladie coronarienne significative détectée (AI-SYNTAX ≤ 2,23). "
            "Aucune revascularisation n'est indiquée selon le score SYNTAX estimé."
        ),
    },
    "mild": {
        "en": (
            "Low SYNTAX score — Mild disease (AI-SYNTAX 2.23–20.92). "
            "Percutaneous coronary intervention (PCI) is preferred if revascularization "
            "is clinically indicated. Cardiology consultation recommended."
        ),
        "fr": (
            "Score SYNTAX faible — Maladie légère (AI-SYNTAX 2,23–20,92). "
            "L'intervention coronarienne percutanée (ICP) est préférée si une "
            "revascularisation est cliniquement indiquée. "
            "Consultation en cardiologie recommandée."
        ),
    },
    "moderate": {
        "en": (
            "Intermediate SYNTAX score — Moderate disease (AI-SYNTAX 20.92–28.25). "
            "Heart Team discussion is recommended to determine the optimal "
            "revascularization strategy (PCI vs. CABG). Cardiology consultation required."
        ),
        "fr": (
            "Score SYNTAX intermédiaire — Maladie modérée (AI-SYNTAX 20,92–28,25). "
            "Une discussion en Heart Team est recommandée pour déterminer la stratégie "
            "optimale de revascularisation (ICP vs. PAC). "
            "Consultation cardiologique requise."
        ),
    },
    "severe": {
        "en": (
            "High SYNTAX score — Severe disease (AI-SYNTAX > 28.25). "
            "Surgical revascularization (CABG) is preferred per current guidelines. "
            "Urgent Heart Team referral is recommended."
        ),
        "fr": (
            "Score SYNTAX élevé — Maladie sévère (AI-SYNTAX > 28,25). "
            "La revascularisation chirurgicale (PAC) est préférée selon les "
            "recommandations actuelles. "
            "Référence urgente au Heart Team recommandée."
        ),
    },
}


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
        """Wrapper forward pass."""

        embeddings: torch.Tensor = self.video_encoder(x)

        if embeddings.ndim == 2:
            embeddings = embeddings.unsqueeze(1)

        elif embeddings.ndim == 3 and embeddings.shape[1] > self.num_videos:
            B, NL, D = embeddings.shape
            if NL % self.num_videos != 0:
                raise ValueError(
                    f"Number of tokens (NL={NL}) is not divisible by the "
                    f"expected num_videos={self.num_videos}. Cannot "
                    "infer tokens per video for hierarchical pooling."
                )
            L = NL // self.num_videos
            embeddings = embeddings.view(B, self.num_videos, L, D)

        if embeddings.ndim == 4:
            B, N, _, _ = embeddings.shape
        else:
            B, N, _ = embeddings.shape

        attention_mask = torch.ones((B, N), dtype=torch.bool, device=embeddings.device)

        return self.mil_model(embeddings, mask=attention_mask)


class CustomPredictionService(BasePredictionService):

    def _get_category_from_threshold(self, regression_value: float) -> str:
        """Convert AI-predicted SYNTAX score to severity category.

        Thresholds are quantile-based, derived from training-set disease
        prevalence (DeepCORO-CLIP SYNTAX paper, JACC-CV):
            ≤ 2.23  → no_disease
            2.23 – 20.92 → mild
            20.92 – 28.25 → moderate
            > 28.25  → severe

        These differ from raw clinical SYNTAX cutoffs (22 / 32 / ≥33) because
        AI-predicted scores have a systematically compressed distribution.

        Args:
            regression_value: AI-predicted SYNTAX score (0–100 scale)

        Returns:
            Category key: 'no_disease', 'mild', 'moderate', or 'severe'
        """
        if regression_value <= SYNTAX_THRESHOLDS["no_disease"]:
            return "no_disease"
        elif regression_value <= SYNTAX_THRESHOLDS["mild"]:
            return "mild"
        elif regression_value <= SYNTAX_THRESHOLDS["moderate"]:
            return "moderate"
        else:
            return "severe"

    def load_model(self, config: Config):
        print("Loading model")

        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return

        class_mapping_path = os.path.join("models", "class_mapping.json")
        with open(class_mapping_path) as fp:
            CustomPredictionService._class_mapping = json.load(fp)

        with open(os.path.join("models", "config.json")) as fp:
            CustomPredictionService.model_config = json.load(fp)

        print(f"Model path: {CustomPredictionService.model_config['ModelStateDict']['model_path']}")
        try:
            print("Loading video encoder")
            video_encoder = VideoEncoder(
                **CustomPredictionService.model_config["VideoEncoder"],
            )

            print("Loading multi-instance linear probing")
            head_structure = {
                head: value["head_dim"]
                for head, value in CustomPredictionService._class_mapping.items()
            }
            mil_model = MultiInstanceLinearProbing(
                **CustomPredictionService.model_config["MultiInstanceLinearProbing"],
                head_structure=head_structure,
            )
            print("Creating video MIL wrapper")
            CustomPredictionService.models["video_mil_wrapper"] = VideoMILWrapper(
                video_encoder,
                mil_model,
                num_videos=CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"],
            )
            print("Successfully created video MIL wrapper")

        except Exception as e2:
            raise RuntimeError(
                f"Cannot initialize model: {str(e2)}"
            ) from e2

        model_state_dict = torch.load(
            os.path.join(
                "models",
                CustomPredictionService.model_config["ModelStateDict"]["model_path"],
            ),
            map_location=torch.device("cpu"),
            weights_only=True,
        )["linear_probing"]
        model_state_dict = {k.replace("module.", ""): v for k, v in model_state_dict.items()}

        CustomPredictionService.models["video_mil_wrapper"].load_state_dict(model_state_dict)
        CustomPredictionService.models["video_mil_wrapper"].eval()
        CustomPredictionService.models["video_mil_wrapper"].to(
            "cuda" if torch.cuda.is_available() else "cpu"
        )
        CustomPredictionService.is_initialized = True
        print(f"Cuda available: {torch.cuda.is_available()}")
        print("Model loaded")

    def _process_predictions(
        self,
        predictions: dict[str, float | torch.Tensor],
    ) -> dict[str, dict]:
        """Process raw model predictions into structured format.

        Returns nested dict:
            {
                "artery_name": {
                    "regression": float,    # AI-predicted SYNTAX score (0–100)
                    "category":   str,      # 'no_disease' | 'mild' | 'moderate' | 'severe'
                }
            }
        """
        class_mapping = CustomPredictionService._class_mapping
        reordered_predictions: dict[str, dict] = {}

        for key in predictions:
            if "category" in key:
                continue

            new_key = class_mapping[key]["name"]
            if new_key not in reordered_predictions:
                reordered_predictions[new_key] = {}

            score_syntax = round(float(predictions[key].item()), 1)
            reordered_predictions[new_key]["regression"] = score_syntax
            reordered_predictions[new_key]["category"] = self._get_category_from_threshold(score_syntax)

        return reordered_predictions

    def _get_diagnosis(self, predictions: dict) -> str:
        """Generate diagnosis string."""
        paragraphs = []

        for syntax_name, values in predictions.items():
            cat = values.get("category", "no_disease")
            label = SYNTAX_CATEGORY_LABELS.get(cat, cat.replace("_", " ").title())
            score = values.get("regression", 0.0)
            paragraphs.append(f"{syntax_name}: {label} — AI-SYNTAX score: {score}")

        return "Cardiac SYNTAX Estimation Summary:\n" + "\n".join(paragraphs)

    def _get_recommendations(self, predictions: dict, language: str = "en") -> str:
        """Generate per-territory clinical recommendations based on SYNTAX category.

        Recommendations follow the simplified revascularisation triage framework
        from the DeepCORO-CLIP SYNTAX paper:
            no_disease → no revascularization
            mild       → PCI preferred
            moderate   → Heart Team discussion
            severe     → CABG preferred + urgent Heart Team referral
        """
        lines = []
        for syntax_name, values in predictions.items():
            cat = values.get("category", "no_disease")
            rec = SYNTAX_RECOMMENDATIONS.get(cat, SYNTAX_RECOMMENDATIONS["no_disease"])
            lines.append(f"{syntax_name}:\n{rec[language]}")

        lines.append(
            "\nNote: This AI analysis is assistive only. Final revascularization "
            "decisions must be made by the clinical team with full patient context."
            if language == "en"
            else "\nNote : Cette analyse IA est uniquement assistive. Les décisions "
            "finales de revascularisation doivent être prises par l'équipe clinique "
            "avec le contexte complet du patient."
        )

        return "\n\n".join(lines)

    def _filter_dicoms_with_metadata(self, dicoms: list[pydicom.Dataset], metadata: dict) -> list[pydicom.Dataset]:
        """Filter DICOMs to keep only Left/Right Coronary with diagnostic status."""
        allowed_views = ["Left Coronary", "Right Coronary"]
        filtered_dicoms = []
        for dicom in dicoms:
            dicom_name = str(dicom.SeriesInstanceUID)
            if dicom_name not in metadata:
                continue
            dicom_meta = metadata[dicom_name]
            if dicom_meta.get("main_structure") not in allowed_views:
                continue
            if dicom_meta.get("status") != "diagnostic":
                continue
            filtered_dicoms.append(dicom)
        return filtered_dicoms

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = []
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                dicom_base64 = request.seriesInstanceImages[series_number][instance_number]
                dicoms.append(pydicom.dcmread(BytesIO(base64.b64decode(dicom_base64))))

        if request.additionalMetadata:
            filtered_dicoms = self._filter_dicoms_with_metadata(
                dicoms, request.additionalMetadata
            )
            print(f"Filtering: {len(dicoms)} total DICOMs, {len(filtered_dicoms)} matched")
            if filtered_dicoms:
                dicoms = filtered_dicoms

        probability = self._run_inference(dicoms)

        if not probability:
            return {
                "htmlBase64": base64.b64encode(
                    b"<h1>No video could be extracted or processed from the current DICOM series</h1>"
                ).decode("utf-8"),
                "diagnosis": "No video could be extracted or processed from the current DICOM series",
                "probability": {},
                "recommendations": {
                    "en": "No video could be extracted or processed from the current DICOM series",
                    "fr": "Aucune vidéo ne peut être extraite ou traitée à partir de la série DICOM actuelle",
                },
            }

        try:
            structured_predictions = self._process_predictions(probability)
        except Exception as e:
            print(f"Error in _process_predictions: {e}")
            structured_predictions = {}

        try:
            diagnosis = self._get_diagnosis(structured_predictions)
        except Exception as e:
            print(f"Error in _get_diagnosis: {e}")
            diagnosis = "Error in _get_diagnosis"

        try:
            recommendations_en = self._get_recommendations(structured_predictions, "en")
            recommendations_fr = self._get_recommendations(structured_predictions, "fr")
        except Exception as e:
            print(f"Error in _get_recommendations: {e}")
            recommendations_en = recommendations_fr = "Error in _get_recommendations"

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

        return {
            "htmlBase64": base64.b64encode(html_output.encode("utf-8")).decode("utf-8")
        }

    async def _handle_json_output(self, request: PredictRequest):
        dicoms = []

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
                        dicoms.append(pydicom.dcmread(BytesIO(dicom_data)))
                    except Exception as e:
                        print(f"Error processing series {series_number} instance {instance_number}: {e}")
                        continue
        except Exception as e:
            print(f"Error in _handle_json_output: {e}")
            return {
                "diagnosis": "Error in _handle_json_output",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in _handle_json_output",
                    "fr": "Erreur dans _handle_json_output",
                    "presentable": True,
                },
            }

        if request.seriesInstanceMetadata:
            filtered_dicoms = self._filter_dicoms_with_metadata(
                dicoms, request.seriesInstanceMetadata
            )
            print(f"Filtering: {len(dicoms)} total, {len(filtered_dicoms)} matched")
            if filtered_dicoms:
                dicoms = filtered_dicoms

        try:
            probability = self._run_inference(dicoms)
        except Exception as e:
            print(f"Error in _run_inference: {e}")
            return {
                "diagnosis": "Error in _run_inference",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in _run_inference",
                    "fr": "Erreur dans _run_inference",
                    "presentable": True,
                },
            }

        try:
            structured_predictions = self._process_predictions(probability)
        except Exception as e:
            print(f"Error in _process_predictions: {e}")
            return {
                "diagnosis": "Error in _process_predictions",
                "predictions": {},
                "modelRecommendations": {
                    "en": "Error in _process_predictions",
                    "fr": "Erreur dans _process_predictions",
                    "presentable": True,
                },
            }

        try:
            diagnosis = self._get_diagnosis(structured_predictions)
        except Exception as e:
            print(f"Error in _get_diagnosis: {e}")
            diagnosis = "Error in _get_diagnosis"

        try:
            recommendations_en = self._get_recommendations(structured_predictions, "en")
            recommendations_fr = self._get_recommendations(structured_predictions, "fr")
        except Exception as e:
            print(f"Error in _get_recommendations: {e}")
            recommendations_en = recommendations_fr = "Error in _get_recommendations"

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
        dicom_name: str = None,
    ) -> Optional[np.ndarray]:
        """Process a DICOM dataset to extract video frames."""
        try:
            input_path = f"{dicom_name}.avi"
            avi_path = process_dicom_video(dicom, input_path)

            if avi_path is None:
                print(f"Failed to process DICOM {dicom_name}")
                return None

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

            try:
                if os.path.exists(input_path):
                    os.remove(input_path)
                if os.path.exists(avi_path):
                    os.remove(avi_path)
            except Exception:
                pass

            return np.asarray(compressedVideo)

        except Exception as e:
            print(f"Error processing DICOM {dicom_name}: {e}")
            return None

    def _run_inference(self, dicoms: list[pydicom.Dataset]) -> dict[str, float]:
        try:
            videos = []
            max_videos = CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"]
            stop_pt = min(len(dicoms), max_videos)
            dicom_ok = 0

            for dicom in dicoms:
                if dicom_ok >= stop_pt:
                    break

                dicom_name = None
                try:
                    dicom_name = dicom.SeriesInstanceUID
                except Exception:
                    dicom_name = f"temp_{uuid.uuid4()}"

                try:
                    video = self.process_dicom_to_video(dicom, dicom_name)
                    if video is None:
                        print(f"Failed to process DICOM {dicom_name}")
                        continue
                except Exception as e:
                    print(f"Error in process_dicom_to_video for {dicom_name}: {e}")
                    continue

                video = video.astype(np.float32)
                video = torch.from_numpy(video)

                if video.shape[-1] in [1, 3]:
                    video = video.permute(0, 3, 1, 2)

                t = video.shape[0]
                expected_frames = CustomPredictionService.model_config["VideoEncoder"]["num_frames"]
                if t < expected_frames:
                    last_frame = video[-1:].repeat(expected_frames - t, 1, 1, 1)
                    video = torch.cat([video, last_frame], dim=0)
                elif t > expected_frames:
                    indices = torch.linspace(0, t - 1, expected_frames).long()
                    video = video[indices]

                video = v2.Resize((224, 224), antialias=True)(video)

                mean = [105.2699966430664, 105.2699966430664, 105.2699966430664]
                std = [39.241127014160156, 39.241127014160156, 39.241127014160156]
                video = v2.Normalize(mean=mean, std=std)(video.float())

                video = video.permute(0, 2, 3, 1).contiguous()
                video = video.cpu().numpy()
                videos.append(video)
                dicom_ok += 1

            video_batch = torch.from_numpy(np.array(videos)).to(dtype=torch.float16)

            if video_batch.shape[0] < max_videos:
                single_video_shape = video_batch.shape[1:]
                padding_shape = (max_videos - video_batch.shape[0],) + single_video_shape
                zero_padding = torch.zeros(padding_shape, dtype=video_batch.dtype, device=video_batch.device)
                video_batch = torch.cat([video_batch, zero_padding], dim=0)

            video_batch = video_batch.unsqueeze(0).to(
                "cuda" if torch.cuda.is_available() else "cpu"
            )

            model = CustomPredictionService.models["video_mil_wrapper"]
            model.eval()

            with torch.no_grad():
                outputs: torch.Tensor = model(video_batch)

            for key in outputs:
                if "category" in key:
                    outputs[key] = torch.argmax(outputs[key], dim=1)
                else:
                    outputs[key] = torch.clamp(
                        torch.round(outputs[key], decimals=1),
                        min=0,
                        max=100,
                    )

            return outputs

        except Exception:
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            return None


def process_dicom_video(
    dicom: pydicom.Dataset,
    output_path: str,
) -> Optional[str]:
    """Convert DICOM video to AVI format."""
    video: np.ndarray = dicom.pixel_array

    if len(video.shape) != 3:
        print(f"Error: Extracted video shape is not 3D: {video.shape}")
        return None

    frame_height: int = dicom[(0x028, 0x0011)].value
    frame_width: int = dicom[(0x028, 0x0010)].value

    if frame_height != video.shape[1]:
        print(f"Error: Height mismatch {frame_height} vs {video.shape[1]}")
        return None

    if frame_width != video.shape[2]:
        print(f"Error: Width mismatch {frame_width} vs {video.shape[2]}")
        return None

    fps: float = 30.0
    if (0x08, 0x2144) in dicom:
        fps = float(dicom[(0x08, 0x2144)].value)

    try:
        photometrics: str = dicom.PhotometricInterpretation
        if photometrics not in ["MONOCHROME1", "MONOCHROME2", "RGB"]:
            print(f"Error: Unsupported Photometric Interpretation: {photometrics}")
            return None
    except Exception:
        print(f"Error reading PhotometricInterpretation from {dicom}")
        return None

    fourcc: int = cv.VideoWriter_fourcc("M", "J", "P", "G")
    out: cv.VideoWriter = cv.VideoWriter(output_path, fourcc, fps, (frame_width, frame_height))

    conversion_fn = (
        cv.COLOR_GRAY2BGR
        if photometrics in ("MONOCHROME1", "MONOCHROME2")
        else cv.COLOR_RGB2BGR
    )
    for frame in dicom.pixel_array:
        out.write(cv.cvtColor(frame, conversion_fn))

    out.release()
    return output_path
