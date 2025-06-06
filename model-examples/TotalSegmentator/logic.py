from io import BytesIO
from totalsegmentator.python_api import totalsegmentator
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

import base64
import numpy as np
import pydicom, pydicom.pixels
import json
import os
import torch
import math

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

    def _get_orientation_type(self, orientation):
        """
        Determine image orientation based on ImageOrientationPatient
        
        Returns:
            str: 'AXIAL', 'SAGITTAL', or 'CORONAL'
        """
        # Extract direction cosines
        row_x, row_y, row_z, col_x, col_y, col_z = orientation
        
        # Calculate normal vector (cross product of row and column)
        normal_x = row_y * col_z - row_z * col_y
        normal_y = row_z * col_x - row_x * col_z
        normal_z = row_x * col_y - row_y * col_x
        
        # Normalize the vector
        magnitude = math.sqrt(normal_x**2 + normal_y**2 + normal_z**2)
        if magnitude == 0:
            return 'UNKNOWN'
            
        normal_x /= magnitude
        normal_y /= magnitude
        normal_z /= magnitude
        
        # Determine orientation by checking which axis the normal vector is most aligned with
        abs_x, abs_y, abs_z = abs(normal_x), abs(normal_y), abs(normal_z)
        
        if abs_z > abs_x and abs_z > abs_y:
            return 'AXIAL'  # Normal is along Z axis
        elif abs_x > abs_y and abs_x > abs_z:
            return 'SAGITTAL'  # Normal is along X axis
        elif abs_y > abs_x and abs_y > abs_z:
            return 'CORONAL'  # Normal is along Y axis
        else:
            return 'UNKNOWN'

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
            
            # Sort instances by InstanceNumber before processing
            instances.sort(key=lambda x: x.InstanceNumber)
                
            # Get orientation information from first instance
            orientation = instances[0].ImageOrientationPatient
            orientation_type = self._get_orientation_type(orientation)
            
            scan = dicom2nifti.convert_dicom.dicom_array_to_nifti(instances, 'ct.nii.gz', reorient_nifti=True)
            pred = CustomPredictionService.models['totalsegmentator'](scan['NII'], device="gpu", fastest=True, task="total") 

            print(f"Orientation: {orientation_type}")
            
            # Apply orientation-specific transformations
            data = pred.get_fdata()
            
            if orientation_type == 'AXIAL':
                # For axial, transpose (2, 1, 0) to get z,y,x
                labelmap_data = np.fliplr(data.transpose(2, 1, 0))
            elif orientation_type == 'SAGITTAL':
                # For sagittal, different transposing pattern needed
                labelmap_data = np.flip(data.transpose(0, 2, 1), axis=(1,2))
            elif orientation_type == 'CORONAL':
                # For coronal, different transposing pattern needed
                labelmap_data = np.flip(data.transpose(1, 2, 0), axis=(0,1))
            else:
                # Fallback to axial-like processing
                labelmap_data = np.fliplr(data.transpose(2, 1, 0))
            
            if orientation_type == 'AXIAL' and instances[0].ImagePositionPatient[2] > instances[1].ImagePositionPatient[2]:
                print("Flipping axial")
                labelmap_data = np.flipud(labelmap_data)

            #TODO: Might need to flipud the orientation if the orientation is not axial
            
            print(f"Output shape (z,y,x): {labelmap_data.shape}")
            
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