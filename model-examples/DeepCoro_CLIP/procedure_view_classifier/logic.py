import os
import uuid
import json
import torch
import base64
import pydicom
import cv2 as cv
import numpy as np
import pandas as pd

from io import BytesIO
from typing import List
from datetime import time
from torchvision.transforms import v2

from models.vaso_vision import VasoVision

from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        print('Loading model...')
        
        CustomPredictionService.config = config

        with open(os.path.join('models', config.class_mapping)) as f:
            class_mapping = json.load(f)

        CustomPredictionService.class_mapping = {}
        for key, value in class_mapping.items():
            CustomPredictionService.class_mapping[key] = {v:k for k, v in value.items()}      
        
        # Create and load the model
        try:
            CustomPredictionService.model_path = os.path.join(
                'models', CustomPredictionService.config.model_path
            )

            CustomPredictionService.models['vaso_vision'] = VasoVision(
                CustomPredictionService.model_path,
                CustomPredictionService.config.head_structure.model_dump()
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
            if dicom[128:132] in [b'DICM']:
                return True
                
            return False
        except Exception:
            return False
 
 
    def _assign_procedure_status(self, predictions: list[dict[str, time | int | None]]) -> list[dict[str, time | int | None]]:
        """
        Assign procedure status based on PCI (stent) presence and timing.
        
        This function creates three mutually-exclusive status categories:
        - "PCI": Current procedure has stent placement
        - "POST_PCI": Current procedure is after a previous PCI in the same study/artery
        - "diagnostic": Diagnostic procedure with no previous PCI
        
        Args:
            predictions: Input list of predictions with series_time and dicom_name
        
        Returns:
            DataFrame with added 'status' column
        """
        print("Assigning procedure status based on PCI timing...")        
        df = pd.DataFrame(predictions, columns=predictions[0].keys())

        # 1. Parse and sort by time (safe)
        df["series_time_dt"] = pd.to_datetime(
            df["series_time"],
            format="%H:%M:%S.%f",
            errors="coerce"
        )
        df = df.sort_values("series_time_dt", kind="mergesort")
                
        # ── 1. Ensure the column exists up front ────────────────────────────────────────
        df["status"] = "unknown"          # will be overwritten below
        
        # 2. PCI flag
        df["is_pci"] = df["stent_presence"].eq('present')

        # 3. PCI already been seen *earlier* for this artery?"
        df["pci_seen_before"] = (
            df.groupby("main_structure", sort=False)["is_pci"]
            .transform(lambda x: x.shift(fill_value=0).cummax())
            .astype(bool)
        )

        # 4. Assign status
        df["status"] = "diagnostic"
        df.loc[df["is_pci"], "status"] = "PCI"
        df.loc[
            (~df["is_pci"]) &
            (df["pci_seen_before"]) &
            (df["contrast_agent"].eq("yes")),
            "status"
        ] = "POST_PCI"
        
        return df.drop(
                columns=["series_time_dt", "is_pci", "pci_seen_before"]
        ).to_dict(orient="records")

    async def _handle_json_output(self, request: PredictRequest)->dict[str, dict[str, str]]:
        dicoms = []
        dicom_names = []

        try:
            for series_number in request.seriesInstanceImages:
                print(f"Processing series {series_number}")
                for dicom_name in request.seriesInstanceImages[series_number]:
                    try: 
                        dicom_base64 = request.seriesInstanceImages[series_number][dicom_name]
                        
                        if not self._is_valid_base64(dicom_base64):
                            print(f"Invalid base64 string for series {series_number} dicom {dicom_name}")
                            continue
                        
                        dicom_data = base64.b64decode(dicom_base64)
                        if not self._is_valid_dicom(dicom_data):
                            print(f"Invalid DICOM data for series {series_number} dicom {dicom_name}")
                            continue
                                            
                        dicom = pydicom.dcmread(BytesIO(dicom_data))
                        dicoms.append(dicom)
                        dicom_names.append(str(dicom.SeriesInstanceUID))
                        
                    except Exception as e:
                        error_msg = f"Error in processing series {series_number} dicom {dicom_name}: {e}"
                        print(error_msg)
                        continue
                    
        except Exception as e:
            error_msg = f"Error in _handle_json_output: {e}"
            print(error_msg)
            return {
                "predictions": []
            }
        
        try:
            structured_predictions: dict[str, dict[str, np.ndarray]] = self._run_inference(dicoms, dicom_names)
        except Exception as e:
            print(f"Error in _run_inference: {e}")
            return {
                "predictions": []
            }
        
        try:
            structured_predictions = self._assign_procedure_status(predictions=structured_predictions)
        except Exception as e:
            print(f"Error in _process_predictions: {e}")
            return {
                "predictions": []
            }
            
        return {
            "predictions": structured_predictions,  # Use the dict, not the JSON string
        }

    def _videoShenanigans(self, video)->np.ndarray:
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

    def _process_dicom(self, dicom: pydicom.Dataset) -> np.ndarray:
        pixel_array: np.ndarray = dicom.pixel_array
        if pixel_array.ndim == 1 or pixel_array.ndim == 2:
            return None
        
        if pixel_array.ndim == 3:
            # Expand single channel to 3 channels by repeating
            pixel_array = np.expand_dims(pixel_array, axis=1)  # Shape: F,1,W,H
            pixel_array = np.repeat(pixel_array, 3, axis=1)    # Shape: F,3,W,H
            
        assert pixel_array.ndim == 4, "Pixel array must have 4 dimensions"
        
        pixel_array = self._videoShenanigans(pixel_array.transpose(0, 2, 3, 1)).astype(np.uint8)
        pixel_array = pixel_array.astype(np.float32)
        
        mean = CustomPredictionService.config.mean
        std = CustomPredictionService.config.std
        
        resize = CustomPredictionService.config.resize
        
        # Convert numpy array to torch tensor and ensure float type
        video = torch.from_numpy(pixel_array)
        video = v2.Resize((resize, resize), antialias=None)(video)
        video = v2.Normalize(mean, std)(video)
        video = video.permute(1, 0, 2, 3)
        video = video.numpy()
        
        c, f, h, w = video.shape
        length = CustomPredictionService.config.frames
        if f < length * 2:
            video = np.concatenate((video, np.zeros((c, length * 2 - f, h, w), video.dtype)), axis=1)
            c, f, h, w = video.shape
        start = np.array([0])
        video = tuple(video[:, s + 2 * np.arange(length), :, :] for s in start)[0]
        
        return torch.from_numpy(video)

    def _extract_series_time(self, dicom: pydicom.Dataset) -> time | None:
        series_time_raw: str | None = dicom.get('SeriesTime')
        if not series_time_raw:
            return None
        try:
            if '.' in series_time_raw:
                main_part, frac = series_time_raw.split('.')
                microseconds = int(frac.ljust(6, '0')[:6])
            else:
                main_part = series_time_raw
                microseconds = 0
            hours = int(main_part[:2])
            minutes = int(main_part[2:4])
            seconds = int(main_part[4:6])
            return time(hours, minutes, seconds, microseconds)
        except (ValueError, IndexError):
            return None

    def _run_inference(self, dicoms: List[pydicom.Dataset], dicom_names: List[str]) -> list[dict[str, str]]:
        try:
            video_stack: list[torch.Tensor] = []
            dicom_metadata_stack: list[dict[str, time | str | None]] = []
            for dicom, dicom_name in zip(dicoms, dicom_names):
                series_time = self._extract_series_time(dicom)
                
                video = self._process_dicom(dicom)
                if video is None:
                    continue
                
                video_stack.append(video)

                dicom_metadata_stack.append({
                    'series_time': series_time,
                    'dicom_name': dicom_name
                })           
                                
            if not video_stack:
                return []
            video_stack = torch.stack(video_stack).to('cuda' if torch.cuda.is_available() else 'cpu')
            with torch.no_grad():
                output_stack: dict[str, torch.Tensor] = CustomPredictionService.models['vaso_vision'](video_stack)
                
                for head_name, head_output in output_stack.items():
                    if getattr(CustomPredictionService.config.head_structure, head_name) == 1:
                        output_stack[head_name] = torch.sigmoid(head_output).to('cpu').detach().numpy()
                    else:
                        output_stack[head_name] = torch.softmax(head_output, dim=1).to('cpu').detach().numpy()
            
            # Transpose {head : [B, P] -> [{head: [P]} for each B]}
            results = []
            class_mapping = CustomPredictionService.class_mapping
            for i in range(video_stack.shape[0]):
                result = {}
                for head_name, head_output in output_stack.items():
                    if getattr(CustomPredictionService.config.head_structure, head_name) == 1:
                        predicted_class = 1 if head_output[i][0] >= 0.5 else 0
                        result[head_name] = class_mapping[head_name][predicted_class]
                    else:
                        result[head_name] = class_mapping[head_name][np.argmax(head_output[i])]
                
                # Add metadata to result
                result['series_time'] = dicom_metadata_stack[i]['series_time'].isoformat() if dicom_metadata_stack[i]['series_time'] is not None else None
                result['dicom_name'] = dicom_metadata_stack[i]['dicom_name']
                
                results.append(result)
                
            return results

        except Exception as e:
            print(f"Error in handler: {e}")
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise e