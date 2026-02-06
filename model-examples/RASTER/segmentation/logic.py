from io import BytesIO
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

import base64
import enum
import nibabel as nib
import numpy as np
from pydicom import dcmread
import typing
import os
import torch
import uuid

import tempfile
from nibabel.nifti1 import Nifti1Image
import dicom2nifti
import dicom2nifti.settings as settings
import math


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
            medical.affine[:3, direction.value],
        )

    return distance_fn


class RASTERRunner:
    def __init__(self, code_dir: str):
        self.code_dir = code_dir
        self.temp_dir = tempfile.gettempdir()
        self.input_dir = None
        self.uuid = None

    def _create_input(self, img: Nifti1Image, subject_id: str) -> str:
        self.uuid = uuid.uuid4().hex
        self.input_dir = os.path.join(self.temp_dir, self.uuid, "input")
        subj_dir = os.path.join(self.input_dir, subject_id)
        os.makedirs(subj_dir)
        img_path = os.path.join(subj_dir, "ct.nii.gz")
        nib.save(img, img_path)

    def run(self, img: Nifti1Image, subject_id: str, subject_name: str) -> None:
        self._create_input(img, subject_id)
        options = ""
        if subject_id != "" or subject_id is not None:
            options += f" --patient_id '{subject_id}'"
        if subject_name != "" or subject_name is not None:
            options += f" --patient_name '{subject_name}'"
        os.system(
            f"nextflow run {self.code_dir}/main.nf --input {self.input_dir} --output_dir {self.temp_dir}/results {options}"
        )
        self.output_path = os.path.join(
            self.temp_dir,
            "results",
            subject_id,
            "STROKE_SEGMENTATION",
            f"{subject_id}_labels.nii.gz",
        )

    def get_output(self, orientation_type: str, instances) -> str:
        img = nib.load(self.output_path)
        print(f"Orientation: {orientation_type}")

        # Apply orientation-specific transformations
        data = img.get_fdata()

        if orientation_type == "AXIAL":
            # For axial, transpose (2, 1, 0) to get z,y,x
            labelmap_data = np.fliplr(data.transpose(2, 1, 0))
        elif orientation_type == "SAGITTAL":
            # For sagittal, different transposing pattern needed
            labelmap_data = np.flip(data.transpose(0, 2, 1), axis=(1, 2))
        elif orientation_type == "CORONAL":
            # For coronal, different transposing pattern needed
            labelmap_data = np.flip(data.transpose(1, 2, 0), axis=(0, 1))
        else:
            # Fallback to axial-like processing
            labelmap_data = np.fliplr(data.transpose(2, 1, 0))

        if (
            orientation_type == "AXIAL"
            and instances[0].ImagePositionPatient[2]
            > instances[1].ImagePositionPatient[2]
        ):
            labelmap_data = np.flipud(labelmap_data)

        # TODO: Might need to flipud the orientation if the orientation is not axial

        print(f"Output shape (z,y,x): {labelmap_data.shape}")

        labelmap_uint8 = labelmap_data.astype(np.uint8)
        encoded_data = base64.b64encode(labelmap_uint8.tobytes()).decode("utf-8")

        # Create segments dictionary only for segments that are present in the data
        segments = {"EDH": 1, "IPH": 2, "IVH": 3, "SAH": 4, "SDH": 5}
        return encoded_data, list(labelmap_data.shape), segments


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        model_dir = os.path.join(os.getcwd(), config.modelDirectory)

        CustomPredictionService.models["RASTER"] = RASTERRunner(model_dir)
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
            return "UNKNOWN"

        normal_x /= magnitude
        normal_y /= magnitude
        normal_z /= magnitude

        # Determine orientation by checking which axis the normal vector is most aligned with
        abs_x, abs_y, abs_z = abs(normal_x), abs(normal_y), abs(normal_z)

        if abs_z > abs_x and abs_z > abs_y:
            return "AXIAL"  # Normal is along Z axis
        elif abs_x > abs_y and abs_x > abs_z:
            return "SAGITTAL"  # Normal is along X axis
        elif abs_y > abs_x and abs_y > abs_z:
            return "CORONAL"  # Normal is along Y axis
        else:
            return "UNKNOWN"

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
            # Parse volumetric DICOM instances into a Nifti Image (From TotalSegmentator)
            instances = []
            for instance in dicoms:
                ds = dcmread(BytesIO(instance), force=True)
                instances.append(ds)

            # Sort instances by InstanceNumber before processing
            instances.sort(key=lambda x: x.InstanceNumber)

            # Get orientation information from first instance
            orientation = instances[0].ImageOrientationPatient
            orientation_type = self._get_orientation_type(orientation)

            # Extract metadata from the first instance
            patient_id = instances[0].PatientID
            patient_name = instances[0].PatientName

            # Convert DICOM instances to Nifti format
            scan = dicom2nifti.convert_dicom.dicom_array_to_nifti(
                instances, "ct.nii.gz", reorient_nifti=True
            )

            # Run RASTER
            RASTER = CustomPredictionService.models["RASTER"]
            RASTER.run(scan, subject_id=patient_id, subject_name=patient_name)
            encoded_data, dimensions, segments = RASTER.get_output(
                orientation_type, instances
            )

            # Create the response dictionary
            response_data = {
                "segmentation": {
                    "labelmap": encoded_data,
                    "dimensions": dimensions,
                    "label": "RASTER - Task: Hemorrhage Segmentation",
                    "segments": segments,
                },
                "measurements": [],  # Empty for now
            }

            # Clean up GPU memory
            if torch.cuda.is_available():
                # Clear any cached tensors
                torch.cuda.empty_cache()
                # Delete large objects explicitly
                del RASTER, scan, encoded_data, dimensions, segments
                torch.cuda.empty_cache()  # Second empty_cache after deletions

            return response_data
        except Exception as e:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise
