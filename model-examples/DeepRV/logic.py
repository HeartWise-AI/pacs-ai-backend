import os
import uuid
import torch
import base64
import pydicom
import cv2 as cv
import numpy as np
import torch.nn as nn

from io import BytesIO
from typing import List
from torchvision.transforms import v2

from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest
from utils.genericLogic import BasePredictionService

from heartwise_statplots.utils import HuggingFaceWrapper


class RegressionHead(nn.Module):
    def __init__(self, dim_in, num_classes=1):
        super().__init__()
        self.fc1 = nn.Conv3d(dim_in, 2048, bias=True, kernel_size=1, stride=1)
        self.regress = nn.Linear(2048, num_classes)

    def forward(self, x):
        x = self.fc1(x)
        x = x.mean([2, 3, 4])
        x = self.regress(x)
        return x

class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        print('Loading model')
        # Retrieve the hugging face token from the environment variable.
        hugging_face_token = os.getenv("HF_API_KEY")
        if not hugging_face_token:
            raise ValueError("HF_API_KEY environment variable is not set")
        
        CustomPredictionService.huggingface_token = hugging_face_token
        CustomPredictionService.repo_id = "heartwise/DeepRV_x3d"
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return 
        CustomPredictionService.model_path = HuggingFaceWrapper.get_model(
            repo_id=CustomPredictionService.repo_id,
            local_dir='models',
            hugging_face_api_key=CustomPredictionService.huggingface_token
        )
        CustomPredictionService.model_path = os.path.join(CustomPredictionService.model_path, 'deeprv_x3d.pt')

        CustomPredictionService.models['x3d_m'] = torch.hub.load(
            "facebookresearch/pytorchvideo", "x3d_m", pretrained=True
        )
        CustomPredictionService.models['x3d_m'].blocks[-1] = RegressionHead(dim_in=192, num_classes=1)

        model_state_dict = torch.load(CustomPredictionService.model_path, map_location=torch.device('cpu'), weights_only=True)['model_state_dict']
        torch.nn.modules.utils.consume_prefix_in_state_dict_if_present(model_state_dict, '_orig_mod.module.')
        CustomPredictionService.models['x3d_m'].load_state_dict(model_state_dict)
        CustomPredictionService.models['x3d_m'].eval()
        CustomPredictionService.models['x3d_m'].to('cuda' if torch.cuda.is_available() else 'cpu')
        CustomPredictionService.is_initialized = True
        print('Model loaded')
        
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
        
        return self.handler(dicoms)

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

    def handler(self, dicoms: List[pydicom.Dataset]):
        try:
            for dicom in dicoms:
                pixel_array: np.ndarray = dicom.pixel_array
                if pixel_array.ndim == 1 or pixel_array.ndim == 2:
                    continue
                
                if pixel_array.ndim == 3:
                    # Expand single channel to 3 channels by repeating
                    pixel_array = np.expand_dims(pixel_array, axis=1)  # Shape: F,1,W,H
                    pixel_array = np.repeat(pixel_array, 3, axis=1)    # Shape: F,3,W,H
                    
                assert pixel_array.ndim == 4, "Pixel array must have 4 dimensions"
                
                pixel_array = self.videoShenanigans(pixel_array.transpose(0, 2, 3, 1)).astype(np.uint8)
                pixel_array = pixel_array.astype(np.float32)
                
                mean = [112.24039459228516, 112.24039459228516, 112.24039459228516]
                std = [39.012229919433594, 39.012229919433594, 39.012229919433594]
                
                # Convert numpy array to torch tensor and ensure float type
                video = torch.from_numpy(pixel_array)
                video = v2.Resize((256, 256), antialias=None)(video)
                video = v2.Normalize(mean, std)(video)
                video = video.permute(1, 0, 2, 3)
                video = video.numpy()
                
                c, f, h, w = video.shape
                length = 72
                if f < length * 2:
                    video = np.concatenate((video, np.zeros((c, length * 2 - f, h, w), video.dtype)), axis=1)
                    c, f, h, w = video.shape
                start = np.array([0])
                video = tuple(video[:, s + 2 * np.arange(length), :, :] for s in start)[0]
                
                video = torch.from_numpy(video)

                # Add batch dimension after processing
                video = video.unsqueeze(0).to('cuda' if torch.cuda.is_available() else 'cpu')
                with torch.no_grad():
                    output: torch.Tensor = CustomPredictionService.models['x3d_m'](video)
                    output = torch.sigmoid(output)  # Add sigmoid activation
                    output: float = output.squeeze(0).detach().cpu().numpy().astype(float)
              
            return {'htmlBase64': HTMLParser.generate_detection_results({'probability': output})}
        
        except Exception as e:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise e