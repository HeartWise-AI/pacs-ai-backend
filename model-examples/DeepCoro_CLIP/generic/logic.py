import base64
import json
import os
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

    def _get_diagnosis(self, predictions: dict[str, float | torch.Tensor]) -> dict[str, str]:
        """Generate diagnosis labels for each prediction head.

        For *binary* heads the label is determined by comparing the predicted
        probability against the class-specific threshold defined in
        `models/class_mapping.json`.

        For *regression* heads the raw value is returned as a percentage
        (0-100%).
        """
        # ------------------------------------------------------------------
        # 1) Lazy-load the class mapping file (only once per process)
        # ------------------------------------------------------------------
        if not hasattr(self.__class__, "_class_mapping"):
            mapping_path = os.path.join("models", "class_mapping.json")
            with open(mapping_path) as fp:
                self.__class__._class_mapping = json.load(fp)
        class_mapping = self.__class__._class_mapping

        diagnoses: dict[str, str] = {}
        for head, value in predictions.items():
            # Convert potential Tensor to a python float
            if isinstance(value, torch.Tensor):
                value = value.item()

            head_cfg = class_mapping.get(head, {})

            # Binary classification head → use threshold
            if "threshold" in head_cfg:
                threshold: float = head_cfg["threshold"]
                diagnoses[head] = "blocked" if value > threshold else "normal"
            else:
                # Regression head → present as percentage with one decimal
                diagnoses[head] = f"{value:.1f}%"

        return diagnoses

    def _get_recommendations(
        self, predictions: dict[str, float | torch.Tensor], language: str
    ) -> str:
        """Generate recommendations based on stenosis predictions and language.

        Args:
            predictions: Dictionary of model predictions (binary and regression heads)
            language: Language code ('en' or 'fr')

        Returns:
            Clinical recommendation string
        """
        # ------------------------------------------------------------------
        # 1) Lazy-load the class mapping file (reuse cached version)
        # ------------------------------------------------------------------
        if not hasattr(self.__class__, "_class_mapping"):
            mapping_path = os.path.join("models", "class_mapping.json")
            with open(mapping_path) as fp:
                self.__class__._class_mapping = json.load(fp)
        class_mapping = self.__class__._class_mapping

        # ------------------------------------------------------------------
        # 2) Mapping for human-readable artery names
        # ------------------------------------------------------------------
        artery_names = {
            "leftmain_stenosis_binary": {"en": "Left Main", "fr": "Tronc Commun Gauche"},
            "lad_stenosis_binary": {
                "en": "LAD (Left Anterior Descending)",
                "fr": "IVA (Interventriculaire Antérieure)",
            },
            "mid_lad_stenosis_binary": {"en": "Mid LAD", "fr": "IVA Moyenne"},
            "dist_lad_stenosis_binary": {"en": "Distal LAD", "fr": "IVA Distale"},
            "diagonal_stenosis_binary": {"en": "Diagonal Branch", "fr": "Branche Diagonale"},
            "D2_stenosis_binary": {"en": "D2 Branch", "fr": "Branche D2"},
            "lcx_stenosis_binary": {"en": "LCX (Left Circumflex)", "fr": "Circonflexe Gauche"},
            "dist_lcx_stenosis_binary": {"en": "Distal LCX", "fr": "Circonflexe Distale"},
            "om1_stenosis_binary": {
                "en": "OM1 (Obtuse Marginal 1)",
                "fr": "OM1 (Marginale Obtuse 1)",
            },
            "om2_stenosis_binary": {
                "en": "OM2 (Obtuse Marginal 2)",
                "fr": "OM2 (Marginale Obtuse 2)",
            },
            "bx_stenosis_binary": {"en": "Branch Vessel", "fr": "Branche Vasculaire"},
            "prox_rca_stenosis_binary": {"en": "Proximal RCA", "fr": "CD Proximale"},
            "mid_rca_stenosis_binary": {"en": "Mid RCA", "fr": "CD Moyenne"},
            "dist_rca_stenosis_binary": {"en": "Distal RCA", "fr": "CD Distale"},
            "pda_stenosis_binary": {
                "en": "PDA (Posterior Descending)",
                "fr": "IVP (Interventriculaire Postérieure)",
            },
            "posterolateral_stenosis_binary": {
                "en": "Posterolateral Branch",
                "fr": "Branche Postérolatérale",
            },
        }

        # ------------------------------------------------------------------
        # 3) Check which arteries are above threshold
        # ------------------------------------------------------------------
        blocked_arteries = []

        for head, value in predictions.items():
            # Only process binary classification heads
            if "_binary" not in head:
                continue

            # Convert tensor to float if needed
            prob_value = value.item() if isinstance(value, torch.Tensor) else value

            # Get threshold for this head
            head_cfg = class_mapping.get(head, {})
            threshold = head_cfg.get("threshold")

            if threshold is not None and prob_value > threshold:
                artery_name = artery_names.get(head, {}).get(language, head)
                percentage = prob_value * 100
                blocked_arteries.append((artery_name, percentage))

        # ------------------------------------------------------------------
        # 4) Generate appropriate recommendation
        # ------------------------------------------------------------------
        if blocked_arteries:
            # Sort by severity (highest percentage first)
            blocked_arteries.sort(key=lambda x: x[1], reverse=True)

            if language == "fr":
                arteries_text = ", ".join(
                    [f"{name} ({perc:.1f}%)" for name, perc in blocked_arteries]
                )
                return (
                    f"Sténose coronarienne détectée dans: {arteries_text}. "
                    f"Recommandation: Consulter un cardiologue interventionnel pour "
                    f"évaluation d'une intervention coronarienne percutanée (ICP)."
                )
            # English
            arteries_text = ", ".join(
                [f"{name} ({perc:.1f}%)" for name, perc in blocked_arteries]
            )
            return (
                f"Coronary stenosis detected in: {arteries_text}. "
                f"Recommendation: Consult an interventional cardiologist for "
                f"percutaneous coronary intervention (PCI) evaluation."
            )
        # No significant stenosis detected
        if language == "fr":
            return (
                "Aucune sténose coronarienne significative détectée. "
                "Continuer la surveillance clinique de routine selon les protocoles établis."
            )
        # English
        return (
            "No significant coronary stenosis detected. "
            "Continue routine clinical monitoring per established protocols."
        )

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
                    "fr": "No video could be extracted or processed from the current DICOM series"
                }
            }
            
        # Obtain per-head diagnosis/interpretation
        diagnosis_dict: dict[str, str] = self._get_diagnosis(probability)

        # The API schema (`JsonPredictionResponse`) expects the *diagnosis* field
        # to be a **string**.  We therefore serialise the dictionary into a JSON
        # string so that downstream consumers still get a single text field
        # while retaining full information.
        diagnosis = json.dumps(diagnosis_dict)

        # Convert all tensor values to Python floats for JSON serialization
        predictions_serializable = {}
        for key, value in probability.items():
            if isinstance(value, torch.Tensor):
                predictions_serializable[key] = value.item()
            else:
                predictions_serializable[key] = value

        # Generate recommendations based on stenosis analysis
        recommendations_en = self._get_recommendations(probability, "en")
        recommendations_fr = self._get_recommendations(probability, "fr")

        # Prepare comprehensive data for HTML parser
        html_data = {
            "diagnosis": diagnosis,
            "probability": predictions_serializable,
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
                    "fr": "No video could be extracted or processed from la série DICOM actuelle",
                    "presentable": True,
                }
            }

        # Obtain per-head diagnosis/interpretation
        diagnosis_dict: dict[str, str] = self._get_diagnosis(probability)

        # The API schema (`JsonPredictionResponse`) expects the *diagnosis* field
        # to be a **string**.  We therefore serialise the dictionary into a JSON
        # string so that downstream consumers still get a single text field
        # while retaining full information.
        diagnosis = json.dumps(diagnosis_dict)

        # Convert all tensor values to Python floats for JSON serialization
        predictions_serializable = {}
        for key, value in probability.items():
            if isinstance(value, torch.Tensor):
                predictions_serializable[key] = value.item()
            else:
                predictions_serializable[key] = value

        # Generate recommendations based on stenosis analysis
        recommendations_en = self._get_recommendations(probability, "en")
        recommendations_fr = self._get_recommendations(probability, "fr")

        return {
            "diagnosis": diagnosis,
            "predictions": predictions_serializable,
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
                if "binary" in key:  # Compute probability output
                    output[key] = torch.sigmoid(output[key])
                else:  # Clamp output to 0-100
                    output[key] = torch.clamp(output[key], min=0, max=100)

            return output

        except Exception:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            
            return None
