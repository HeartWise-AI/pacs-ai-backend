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
    def _get_category_from_threshold(self, regression_value: float) -> str:
        """
        Convert regression value to category based on thresholds.
        
        Args:
            regression_value: The regression score (0-100)
            
        Returns:
            Category string: 'normal', 'low', 'intermediate', or 'high'
        """
        if regression_value <= 2.23:
            return 'normal'
        elif regression_value <= 18.50:
            return 'low'
        elif regression_value <= 22.95:
            return 'intermediate'
        else:
            return 'high'
    
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
            
            if 'category' in key:
                continue
            
            new_key = class_mapping[key]['name']

            if not new_key in reordered_predictions:
                reordered_predictions[new_key] = {}
            
            # All predictions are now regression values with threshold-based categorization
            score_syntax = round(float(predictions[key].item()), 1)
            reordered_predictions[new_key]['regression'] = score_syntax
            reordered_predictions[new_key]['category'] = self._get_category_from_threshold(score_syntax)

        return reordered_predictions

    def _get_diagnosis(self, predictions: dict) -> str:
        """
        Generate diagnosis string in English
        """        
        # Generate paragraphs for each system
        paragraphs = []
        
        def format_syntax_list(predictions: dict, syntax_name: str) -> str:
            """
            Format a list of predictions for a given syntax
            """
            return f"{syntax_name}: {predictions['category']} - Estimated severity: {predictions['regression']}"
        
        # RCA System
        global_paragraph = format_syntax_list(predictions['Global Cardiac Syntax'], 'Global Cardiac Syntax')
        if global_paragraph:
            paragraphs.append(global_paragraph)
        
        # LCA System  
        right_paragraph = format_syntax_list(predictions['Right Cardiac Syntax'], 'Right Cardiac Syntax')
        if right_paragraph:
            paragraphs.append(right_paragraph)
        
        # Other arteries
        left_paragraph = format_syntax_list(predictions['Left Cardiac Syntax'], 'Left Cardiac Syntax')
        if left_paragraph:
            paragraphs.append(left_paragraph)
                
        # Join paragraphs
        return "Cardiac Syntax Detection Summary:\n" + "\n".join(paragraphs)

    def _get_recommendations(self, predictions: dict, language: str = "en") -> str:
        """
        Generate recommendations based on predictions
        """
        recommendations = []
        for syntax_name in predictions.keys():
            recommendations.append(f"{syntax_name} {predictions[syntax_name]['category']}.\n")

        if recommendations:
            recommendations.append(
                "Consult a Cardiologist for further evaluation is recommended." if language == "en" else "Un cardiologue doit être consulté pour une évaluation plus approfondie."
            )
        else:
            recommendations.append("Normal cardiac syntax detected." if language == "en" else "Syntaxe cardiaque normale.")

        return "\n".join(recommendations)

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
        try:    
            structured_predictions: dict[str, dict] = self._process_predictions(probability)
        except Exception as e:
            print(f"Error in _process_predictions: {e}")
            structured_predictions = {
                "diagnosis": "Error in _process_predictions",
                "probability": {},
                "recommendations": {
                    "en": "Error in _process_predictions",
                    "fr": "Erreur dans _process_predictions"
                }
            }
        # Transform into a diagnosis string
        try:    
            diagnosis = self._get_diagnosis(structured_predictions)
        except Exception as e:
            print(f"Error in _get_diagnosis: {e}")
            diagnosis = "Error in _get_diagnosis"

        # # Generate recommendations based on stenosis analysis
        try:
            recommendations_en = self._get_recommendations(structured_predictions, "en")
            recommendations_fr = self._get_recommendations(structured_predictions, "fr")
        except Exception as e:
            print(f"Error in _get_recommendations: {e}")
            recommendations_en = "Error in _get_recommendations"
            recommendations_fr = "Error in _get_recommendations"
        
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

    def _run_inference(self, dicoms: list[pydicom.Dataset]) -> dict[str, float]:
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
                
                # resize
                video = v2.Resize((224, 224), antialias=True)(video)             

                # normalize
                mean = [105.2699966430664, 105.2699966430664, 105.2699966430664]
                std = [39.241127014160156, 39.241127014160156, 39.241127014160156]
                video = v2.Normalize(mean=mean, std=std)(video.float())
                
                # permute
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
                if "category" in key:  # get max
                    outputs[key] = torch.argmax(outputs[key], dim=1)
                else:  # Clamp output to 0-100
                    outputs[key] = torch.clamp(
                        torch.round(outputs[key], decimals=1),
                        min=0,
                        max=100
                    )
            
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
