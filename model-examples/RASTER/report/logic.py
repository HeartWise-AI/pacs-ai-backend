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


class RASTERRunner:
    def __init__(self, code_dir: str):
        """
        Initialize the class with the specified code directory.

        Args:
            code_dir (str): The directory path containing the code files.

        Attributes:
            code_dir (str): The directory path for code files.
            temp_dir (str): The system's temporary directory path.
            input_dir (None): Input directory path, initially set to None.
            uuid (None): Unique identifier, initially set to None.
        """
        self.code_dir = code_dir
        self.temp_dir = tempfile.gettempdir()
        self.input_dir = None
        self.uuid = None

    def _create_input(self, img: Nifti1Image, subject_id: str) -> None:
        """
        Create input directory structure and save NIfTI image for processing.

        This method sets up a temporary directory structure with a unique UUID and saves
        the provided NIfTI image to the appropriate location for further processing.

        Args:
            img (Nifti1Image): The NIfTI image to be saved for processing.
            subject_id (str): Unique identifier for the subject/patient.

        Returns:
            None
        """
        self.uuid = uuid.uuid4().hex
        self.input_dir = os.path.join(self.temp_dir, self.uuid, "input")
        subj_dir = os.path.join(self.input_dir, subject_id)
        os.makedirs(subj_dir)
        img_path = os.path.join(subj_dir, "ct.nii.gz")
        nib.save(img, img_path)

    def run(self, img: Nifti1Image, subject_id: str, subject_name: str) -> None:
        """
        Execute the volumetry report generation pipeline for a given CT image.

        This method processes a NIfTI medical image through a Nextflow pipeline to generate
        a volumetry report. It creates the necessary input files, constructs command-line
        options, runs the pipeline, and sets the output path for the generated PDF report.

        Args:
            img (Nifti1Image): The input CT image in NIfTI format to be processed.
            subject_id (str): Unique identifier for the subject/patient.
            subject_name (str): Human-readable name of the subject/patient.

        Returns:
            None
        """
        self._create_input(img, subject_id)
        options = ""
        if subject_id != "" and subject_id is not None:
            options += f" --patient_id '{subject_id}'"
        if subject_name != "" and subject_name is not None:
            options += f" --patient_name '{subject_name}'"
        os.system(
            f"nextflow run {self.code_dir}/main.nf --input {self.input_dir} --output_dir {self.temp_dir}/results {options}"
        )
        self.output_path = os.path.join(
            self.temp_dir,
            "results",
            subject_id,
            "VOLUMETRY_REPORT",
            f"{subject_id}__volumetry_report.pdf",
        )

    def get_output(self) -> str:
        """
        Retrieve the base64-encoded content of the output file.

        Returns:
            str: The base64-encoded string representation of the output file content.
        """
        if not os.path.exists(self.output_path):
            raise FileNotFoundError(f"Output file not found at {self.output_path}")
        with open(self.output_path, "rb") as f:
            return base64.b64encode(f.read()).decode("utf-8")


class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        """
        Load and initialize the RASTER model.

        Args:
            config (Config): Configuration object containing the model directory path.

        This method sets up the RASTER model by creating an instance of the RASTERRunner
        class and storing it in the models dictionary. It also marks the service as initialized.
        """
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
            A dictionary containing the pdf data with pdfBase64 definitions.
        """
        try:
            instances = []
            for instance in dicoms:
                ds = dcmread(BytesIO(instance), force=True)
                instances.append(ds)

            # Sort instances by InstanceNumber before processing
            instances.sort(key=lambda x: x.InstanceNumber)

            # Extract metadata from the first instance
            patient_id = instances[0].PatientID
            patient_name = instances[0].PatientName

            # Convert DICOM instances to NIfTI
            scan = dicom2nifti.convert_dicom.dicom_array_to_nifti(
                instances, "ct.nii.gz", reorient_nifti=True
            )

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
                del RASTER, scan
                torch.cuda.empty_cache()  # Second empty_cache after deletions

            return response_data
        except Exception as e:
            # Make sure to clean GPU memory even if there's an error
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise
