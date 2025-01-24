from io import BytesIO
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

import base64
import enum
import nibabel as nib
import numpy as np
import pydicom, pydicom.pixels
import typing
import os
import torch

import tempfile
from nibabel.nifti1 import Nifti1Image


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

    def _create_input(self, img: Nifti1Image, subject_id: str) -> str:
        self.input_dir = os.path.join(self.temp_dir, "input", subject_id)
        os.mkdir(self.input_dir)
        img_path = os.path.join(self.input_dir, "ct.nii.gz")
        nib.save(img, img_path)
        return img_path

    def run(self, img: Nifti1Image, subject_id: str, subject_name: str) -> Nifti1Image:
        input_path = self._create_input(img, subject_id)
        options = ""
        if subject_id != "" or subject_id is not None:
            options += f" --patient_id '{subject_id}'"
        if subject_name != "" or subject_name is not None:
            options += f" --patient_name '{subject_name}'"
        os.system(
            f"nextflow run {self.code_dir}/main.nf --input {input_path} --output_dir {self.temp_dir}/results {options}"
        )
        self.output_path = os.path.join(
            self.temp_dir,
            "results",
            subject_id,
            "VOLUMETRY_REPORT",
            f"{subject_id}__volumetry_report.pdf",
        )

    def get_output(self) -> str:
        with open(self.output_path, "rb") as f:
            return base64.b64encode(f.read()).decode("utf-8")


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        model_dir = os.path.join(os.getcwd(), config.modelDirectory)

        CustomPredictionService.models["RASTER"] = RASTERRunner(model_dir)
        CustomPredictionService.is_initialized = True

    async def _handle_pdf_output(self, request: PredictRequest):
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
            # Parse volumetric DICOM instances into a Nifti Image (From TotalSegmentator)
            instances = []
            patient_id = patient_name = "Not Available"
            for instance in dicoms:
                ds = pydicom.dcmread(BytesIO(instance), force=True)
                if patient_name == "Not Available" and hasattr(ds, "PatientName"):
                    patient_name = ds.PatientName
                if patient_id == "Not Available" and hasattr(ds, "PatientID"):
                    patient_id = ds.PatientID
                # Ensure proper pixel units for modalities like CT (Hounsfield unit)
                image = pydicom.pixels.apply_modality_lut(ds.pixel_array, ds)
                # Reorder slice dimensions as an eventual channel-first 3D
                # volume to support both volumetric and RGB images.
                # TODO: are 3D JPEG possible in medical imaging?
                match (
                    getattr(ds, "NumberOfFrame", 1),
                    getattr(ds, "SamplesPerPixel", 1),
                ):
                    case 1, 1:
                        image = np.expand_dims(image, (0, -1))
                    case 1, 3:
                        image = np.expand_dims(np.moveaxis(image, 2, 0), 3)
                    case _, 1:
                        image = np.expand_dims(np.moveaxis(image, 0, 2), 0)
                    case _, 3:
                        image = np.moveaxis(image, [0, 3], [3, 0])
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
            volume_squeezed = volume.squeeze(
                0
            )  # remove channel dim for Nifti-based pipelines
            scan = nib.Nifti1Image(volume_squeezed, affine)

            # Run RASTER
            RASTER = CustomPredictionService.models["RASTER"]
            RASTER.run(scan, subject_id=patient_id, subject_name=patient_name)

            # Create the response dictionary
            response_data = {"pdfBase64": RASTER.get_output()}

            # Clean up GPU memory
            if torch.cuda.is_available():
                # Clear any cached tensors
                torch.cuda.empty_cache()
                # Delete large objects explicitly
                del RASTER, scan, volume, volume_squeezed
                torch.cuda.empty_cache()  # Second empty_cache after deletions

            return response_data
        except Exception as e:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise
