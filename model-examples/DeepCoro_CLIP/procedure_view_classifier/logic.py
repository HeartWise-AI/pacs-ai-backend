import os
import uuid
import json
import torch
import base64
import pydicom
import cv2 as cv
import numpy as np
import re

from io import BytesIO
from typing import List
from datetime import time
from torchvision.transforms import v2

from models.vaso_vision import VasoVision

from utils.dicom_pixels import dicom_display_pixels
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            return
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
 
 
    def _assign_procedure_status(self, predictions: list[dict]) -> list[dict]:
        """Apply the existing PCI chronology rule to the COMPLETE request.

        Missing/malformed time or tied PCI/non-PCI timing cannot safely establish
        diagnostic status, so fail rather than assigning a reassuring default.
        """
        def timestamp(row):
            value = row.get("series_time")
            if not isinstance(value, str) or not re.fullmatch(r"\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?", value):
                raise ValueError("Complete SeriesTime is required for procedure classification")
            return time.fromisoformat(value)

        ordered = sorted(predictions, key=lambda row: (timestamp(row), row["sop_instance_uid"]))
        by_time = {}
        for row in ordered:
            if row.get("stent_presence") not in {"present", "absent"} or row.get("contrast_agent") not in {"yes", "no"} or not isinstance(row.get("main_structure"), str):
                raise ValueError("Classifier cannot establish a complete procedure label")
            key = (row.get("main_structure"), timestamp(row))
            by_time.setdefault(key, set()).add(row.get("stent_presence"))
        if any("present" in states and "absent" in states for states in by_time.values()):
            raise ValueError("Ambiguous PCI timing cannot establish diagnostic status")

        pci_seen = set()
        for row in ordered:
            main = row.get("main_structure")
            if row.get("stent_presence") == "present":
                row["status"] = "PCI"
                pci_seen.add(main)
            elif main in pci_seen and row.get("contrast_agent") == "yes":
                row["status"] = "POST_PCI"
            else:
                row["status"] = "diagnostic"
        return ordered

    async def _handle_json_output(self, request: PredictRequest) -> dict:
        dicoms, identities = [], set()
        for series in request.seriesInstanceImages.values():
            for encoded in series.values():
                try:
                    raw = base64.b64decode(encoded, validate=True)
                    if not self._is_valid_dicom(raw):
                        raise ValueError("Invalid DICOM")
                    dicom = pydicom.dcmread(BytesIO(raw))
                    identity = (str(dicom.SeriesInstanceUID), str(dicom.SOPInstanceUID))
                    if not all(identity) or identity[1] in identities:
                        raise ValueError("Missing or duplicate SOP identity")
                    identities.add(identity[1])
                    dicoms.append(dicom)
                except Exception as exc:
                    raise ValueError("Classifier requires valid, uniquely identified DICOM inputs") from exc
        if not dicoms:
            raise ValueError("Classifier requires at least one video")
        predictions = self._run_inference(dicoms, [str(d.SeriesInstanceUID) for d in dicoms])
        return {"predictions": self._assign_procedure_status(predictions)}

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
        pixel_array: np.ndarray = dicom_display_pixels(dicom)
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
        series_time_raw = str(series_time_raw).strip()
        if not re.fullmatch(r"\d{6}(?:\.\d{1,6})?", series_time_raw):
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

    def _run_inference(self, dicoms: List[pydicom.Dataset], dicom_names: List[str]) -> list[dict]:
        """Bound GPU forwards to one video; retain every prediction for chronology.

        Pixel conversion, temporal sampling, normalization, trained heads and the
        binary threshold are unchanged. No input is silently dropped.
        """
        if len(dicoms) != len(dicom_names):
            raise ValueError("Classifier identity count mismatch")
        results = []
        with torch.no_grad():
            for dicom, dicom_name in zip(dicoms, dicom_names):
                series_time = self._extract_series_time(dicom)
                if series_time is None:
                    raise ValueError("Complete SeriesTime is required for procedure classification")
                series_uid = str(getattr(dicom, "SeriesInstanceUID", ""))
                sop_uid = str(getattr(dicom, "SOPInstanceUID", ""))
                if not series_uid or not sop_uid or series_uid != dicom_name:
                    raise ValueError("Classifier input identity mismatch")
                video = self._process_dicom(dicom)
                if video is None:
                    raise ValueError("Classifier requires multi-frame video inputs")
                batch = video.unsqueeze(0).to("cuda" if torch.cuda.is_available() else "cpu")
                output = CustomPredictionService.models['vaso_vision'](batch)
                result = {}
                for head_name, head_output in output.items():
                    binary = getattr(CustomPredictionService.config.head_structure, head_name) == 1
                    probabilities = (torch.sigmoid(head_output) if binary else torch.softmax(head_output, dim=1)).to('cpu').detach().numpy()
                    if not np.isfinite(probabilities).all():
                        raise ValueError("Classifier returned non-finite probabilities")
                    index = (1 if probabilities[0][0] >= 0.5 else 0) if binary else int(np.argmax(probabilities[0]))
                    try:
                        result[head_name] = CustomPredictionService.class_mapping[head_name][index]
                    except KeyError as exc:
                        raise ValueError("Classifier output does not match its label mapping") from exc
                    if head_name == "dominance" and binary:
                        # Sigmoid of the binary head is P(class 1); the call above stays at >= 0.5.
                        if CustomPredictionService.class_mapping[head_name].get(1) != "left_dominant":
                            raise ValueError("Dominance head does not match its label mapping")
                        result["dominance_left_probability"] = round(float(probabilities[0][0]), 6)
                if not {"main_structure", "contrast_agent", "stent_presence"}.issubset(result):
                    raise ValueError("Classifier output is incomplete")
                result.update({
                    "series_time": series_time.isoformat(),
                    "dicom_name": series_uid,
                    "series_instance_uid": series_uid,
                    "sop_instance_uid": sop_uid,
                })
                results.append(result)
                del batch, output
        return results
