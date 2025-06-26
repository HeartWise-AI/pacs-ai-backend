from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

import json
import os
import torch
import numpy as np
from nnunetv2.inference.predict_from_raw_data import nnUNetPredictor
from nnunetv2.imageio.simpleitk_reader_writer import SimpleITKIO


class CustomPredictionService(BasePredictionService):

    def load_model(self, config: Config):
        workingDirectory = os.getcwd()
        model_dir = config.modelDirectory
        class_mapping_filepath = os.path.join(workingDirectory, model_dir, 'class_mapping.json')
        self.class_mapping = json.load(open(class_mapping_filepath))
        model_folder = os.path.join(workingDirectory, model_dir, 'Dataset005_WinMultiICHv5', 'nnUNetTrainer__nnUNetPlans__3d_fullres')
        use_folds = 0,
        print("Initializing model")
        self.predictor = nnUNetPredictor(
            tile_step_size=0.5,
            use_gaussian=True,
            use_mirroring=True,
            perform_everything_on_device=True,
            device=torch.device('cuda', 0),
            verbose=False,
            verbose_preprocessing=False,
            allow_tqdm=True)
        print("Loading model weights")
        self.predictor.initialize_from_trained_model_folder(
            model_folder,
            use_folds=use_folds,
            checkpoint_name='checkpoint_final.pth')
        CustomPredictionService.models['nnunet'] = self.predictor
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
        nifti_image = series_list_nifti[0]
        
        # Make predictions on the first series (nifti)
        img = np.clip(np.expand_dims(nifti_image.get_fdata().T, 0), -10, 140)
        props = self._create_props_from_nifti_and_dicom(serie[0])
        pred = self.predictor.predict_from_list_of_npy_arrays([img], None, [props], None, 2, save_probabilities=False, num_processes_segmentation_export=2)


        # Reorient Predictions for OHIF display (only accepts 1 pydicom series)
        labelmap_data = self.reorient_labelmap(pred[0].T, serie)

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

    def _create_props_from_nifti_and_dicom(self, dicom):
        """
        Create props object with the required structure for nnUNet predictor.
        
        Args:
            dicom: pydicom instance of Dataset object
            
        Returns:
            Dictionary with 'sitk_stuff' and 'spacing' keys matching the expected structure
        """
        
        # Get pixel spacing (x, y) from DICOM
        if hasattr(dicom, 'PixelSpacing'):
            pixel_spacing = dicom.PixelSpacing
            spacing_x, spacing_y = float(pixel_spacing[1]), float(pixel_spacing[0])  # Note: DICOM PixelSpacing is [row, col]
        else:
            # Fallback to default values if not available
            spacing_x, spacing_y = 0.5, 0.5
        
        # Get slice thickness (z spacing) from DICOM
        spacing_z = float(dicom.SliceThickness)
     
        
        # Get origin from DICOM ImagePositionPatient
        if hasattr(dicom, 'ImagePositionPatient'):
            origin = tuple(float(x) for x in dicom.ImagePositionPatient)
        else:
            origin = (0.0, 0.0, 0.0)
        
        # Get direction from DICOM ImageOrientationPatient
        if hasattr(dicom, 'ImageOrientationPatient'):
            orientation = dicom.ImageOrientationPatient
            # Convert 6-element orientation to 9-element direction matrix
            row_x, row_y, row_z = float(orientation[0]), float(orientation[1]), float(orientation[2])
            col_x, col_y, col_z = float(orientation[3]), float(orientation[4]), float(orientation[5])
            
            # Calculate normal vector (cross product of row and column)
            normal_x = row_y * col_z - row_z * col_y
            normal_y = row_z * col_x - row_x * col_z
            normal_z = row_x * col_y - row_y * col_x
            
            direction = (row_x, row_y, row_z, col_x, col_y, col_z, normal_x, normal_y, normal_z)
        else:
            # Default identity direction
            direction = (1.0, 0.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0, 1.0)
        
        # Create the props object with the expected structure
        props = {
            'sitk_stuff': {
                'spacing': (spacing_x, spacing_y, spacing_z),
                'origin': origin,
                'direction': direction
            },
            'spacing': [spacing_z, spacing_x, spacing_y]  # Note: nnUNet expects [z, y, x] order
        }
        
        return props