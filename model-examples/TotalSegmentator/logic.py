from io import BytesIO
from totalsegmentator.python_api import totalsegmentator
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

import base64
import enum
import nibabel as nib
import numpy as np
import pydicom, pydicom.pixels
import typing
import json
import os
import torch

import dicom2nifti
import dicom2nifti.settings as settings

settings.disable_validate_slice_increment()
settings.enable_resampling()
settings.set_resample_spline_interpolation_order(1)
settings.set_resample_padding(-1000)

class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        workingDirectory = os.getcwd()
        model_dir = config.modelDirectory
        class_mapping_filepath = os.path.join(workingDirectory, model_dir, 'class_mapping.json')
        self.class_mapping = json.load(open(class_mapping_filepath))
        CustomPredictionService.models['totalsegmentator'] = totalsegmentator
        CustomPredictionService.is_initialized = True

    async def _handle_ohif_output(self, request: PredictRequest):
        # Get the first series
        series = next(iter(request.seriesInstanceImages.values()))
        dicoms = []
        for instanceUID, instance in series.items():
            dicoms.append(base64.b64decode(instance))
        
        response = self._handle_ohif(dicoms)
        return response  # Return the dictionary directly

    def _handle_ohif(self, dicoms: dict[int, dict[int, dict]]) -> dict:
        """Parse DICOM instances into Nifti volumetric images and segment.

        Args:
            dicoms: A mapping of series number to a mapping of instance number
                to a DICOMWeb (JSON) encoded DICOM.

        Returns:
            A dictionary containing the segmentation data with labelmap, dimensions,
            label information and segment definitions.
        """
        try:
            instances = []
            for instance in dicoms:
                ds = pydicom.dcmread(BytesIO(instance), force=True)
                instances.append(ds)
            scan = dicom2nifti.convert_dicom.dicom_array_to_nifti(instances, 'ct.nii.gz', reorient_nifti=False)
            pred = CustomPredictionService.models['totalsegmentator'](scan['NII'], device="gpu", fastest=True, task="total") 
            
            labelmap_data = np.fliplr(np.flipud(pred.get_fdata().transpose(2, 1, 0)))
            labelmap_uint8 = labelmap_data.astype(np.uint8)
            encoded_data = base64.b64encode(labelmap_uint8.tobytes()).decode('utf-8')
                
            # Create segments dictionary only for segments that are present in the data
            unique_values = np.unique(labelmap_uint8)
            segments = {name: i for name, i in self.class_mapping.items() if i in unique_values}
            
            # Create the response dictionary
            response_data = {
                "segmentation": {
                    "labelmap": encoded_data,
                    "dimensions": list(labelmap_data.shape),
                    "label": "TotalSegmentator - Task: Total",
                    "segments": segments
                },
                "measurements": []  # Empty for now
            }

                # Clean up GPU memory
            if torch.cuda.is_available():
                # Clear any cached tensors
                torch.cuda.empty_cache()
                # Delete large objects explicitly
                del pred, labelmap_data, scan
                torch.cuda.empty_cache()  # Second empty_cache after deletions

            return response_data
        except Exception as e:
                # Make sure to clean GPU memory even if there's an error
                if torch.cuda.is_available():
                    torch.cuda.empty_cache()
                raise