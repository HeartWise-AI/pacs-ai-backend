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


class MedicalImage(typing.NamedTuple):

    image: np.ndarray
    affine: np.ndarray


class Direction(enum.IntEnum):

    SAGITTAL = 0
    CORONAL = 1
    AXIAL = 2
    ORIGIN = 3


def sorting_direction(direction: Direction) -> typing.Callable[[np.ndarray], float]:
    """A function that computes the distance to the origin along a direction."""
    def distance_fn(medical: MedicalImage) -> float:
        """Distance along an orthonormal direction."""
        return np.inner(
                medical.affine[:3, Direction.ORIGIN.value],
                medical.affine[:3, direction.value])
    return distance_fn


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
        
        response = self.handler(dicoms)
        return response  # Return the dictionary directly

    def handler(self, dicoms: dict[int, dict[int, dict]]) -> dict:
        """Parse DICOM instances into Nifti volumetric images and segment.

        Args:
            dicoms: A mapping of series number to a mapping of instance number
                to a DICOMWeb (JSON) encoded DICOM.

        Returns:
            A dictionary containing the segmentation data with labelmap, dimensions,
            label information and segment definitions.
        """
        try:
            # Parse volumetric DICOM instances into a Nifti Image
            instances = []
            for instance in dicoms:
                ds = pydicom.dcmread(BytesIO(instance), force=True)
                # Ensure proper pixel units for modalities like CT (Hounsfield unit)
                image = pydicom.pixels.apply_modality_lut(ds.pixel_array, ds)
                # Reorder slice dimensions as an eventual channel-first 3D
                # volume to support both volumetric and RGB images.
                # TODO: are 3D JPEG possible in medical imaging?
                match (getattr(ds, "NumberOfFrame", 1),
                       getattr(ds, "SamplesPerPixel", 1)):
                    case 1, 1: image = np.expand_dims(image, (0, -1))
                    case 1, 3: image = np.expand_dims(np.moveaxis(image, 2, 0), 3)
                    case _, 1: image = np.expand_dims(np.moveaxis(image, 0, 2), 0)
                    case _, 3: image = np.moveaxis(image, [0, 3], [3, 0])
                assert image.ndim == 4
                assert image.dtype == np.float64
                orientation = np.reshape(ds.ImageOrientationPatient, [2, 3])
                orientation = np.flipud(orientation)
                inplane = orientation.T * ds.PixelSpacing
                cosine = np.cross(inplane[:, 0], inplane[:, 1])
                affine = np.column_stack([inplane, cosine, ds.ImagePositionPatient])
                affine = np.vstack([affine, [0, 0, 0, 1]])
                instances.append(MedicalImage(image, affine))
            # Sort direction slices using their relative affine-encoded position
            # TODO: don't assume axial acquisitions, parse from DICOM metadata
            slices = sorted(instances, key=sorting_direction(Direction.AXIAL))
            affine = slices[0].affine  # construct whole volume affine from first slice
            cosine = slices[-1].affine[:3, 3] - affine[:3, 3]
            if len(slices) > 1:
                cosine /= len(slices) - 1
            affine[:3, 2] = cosine
            volume = np.concatenate([slice for slice, _ in slices], axis=3)
            volume_squeezed = volume.squeeze(0)  # remove channel dim for Nifti-based pipelines
            scan = nib.Nifti1Image(volume_squeezed, affine)
            # If you change the task/fast/fastest, you need to change the weights in utils/download_pretrained_weights.py
            pred = CustomPredictionService.models['totalsegmentator'](scan, device="gpu", fastest=True, task="total") 
            
            # Get the labelmap data and encode it
            labelmap_data = pred.get_fdata().transpose(2, 0, 1)
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
                del pred, labelmap_data, scan, volume, volume_squeezed
                torch.cuda.empty_cache()  # Second empty_cache after deletions

            return response_data
        except Exception as e:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise
