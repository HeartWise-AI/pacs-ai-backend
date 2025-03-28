import os
import torch
import base64
import pydicom
import torch.nn as nn

from io import BytesIO

from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

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

        model = torch.hub.load(
            "facebookresearch/pytorchvideo", "x3d_m", pretrained=True
        )
        model.blocks[-1] = RegressionHead(dim_in=192, num_classes=1)

        model_state_dict = torch.load(CustomPredictionService.model_path, map_location=torch.device('cpu'), weights_only=True)['model_state_dict']
        torch.nn.modules.utils.consume_prefix_in_state_dict_if_present(model_state_dict, '_orig_mod.module.')
        model.load_state_dict(model_state_dict)
        CustomPredictionService.is_initialized = True
    
    async def _handle_html_output(self, request: PredictRequest):
        print('Handling HTML output')
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
        print(dicoms[0].pixel_array.shape)
        return True, {}