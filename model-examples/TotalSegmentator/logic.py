from totalsegmentator.python_api import totalsegmentator
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

import json
import os
import torch


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        workingDirectory = os.getcwd()
        model_dir = config.modelDirectory
        class_mapping_filepath = os.path.join(workingDirectory, model_dir, 'class_mapping.json')
        self.class_mapping = json.load(open(class_mapping_filepath))
        CustomPredictionService.models['totalsegmentator'] = totalsegmentator
        CustomPredictionService.is_initialized = True

    async def _handle_ohif_annotations_output(self, request: PredictRequest):
        """Parse DICOM instances into Nifti volumetric images and segment.

        Args:
            request: PredictRequest containing series instance images

        Returns:
            A dictionary containing the segmentation data with labelmap, dimensions,
            label information and segment definitions.
        """
        series_list_pydicom, series_list_nifti = self.get_series_from_seriesInstanceImages(request.seriesInstanceImages)

        # Get the first series
        serie = series_list_pydicom[0]

        # Make predictions on the first series (nifti)
        pred = CustomPredictionService.models['totalsegmentator'](series_list_nifti[0], device="gpu", fast=True, task="total") 

        # Reorient Predictions for OHIF display (only accepts 1 pydicom series)
        labelmap_data = self.reorient_labelmap(pred, serie)

        encoded_data, segments = self.encode_labelmap_and_segments(labelmap_data, self.class_mapping)
        
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
            del pred, labelmap_data, serie
            torch.cuda.empty_cache()  # Second empty_cache after deletions

        return response_data