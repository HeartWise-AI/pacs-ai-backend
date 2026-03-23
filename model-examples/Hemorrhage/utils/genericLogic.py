import os
import json
import math
import base64
from typing import Any, Dict, List, Tuple
from io import BytesIO

import numpy as np
import numpy
import nibabel as nib
import pydicom
import pydicom.pixels
import dicom2nifti
from dicom2nifti.image_volume import ImageVolume

from utils.http_utils import HTTPResponse, PredictRequest

import dicom2nifti.settings as settings

settings.disable_validate_slice_increment()
settings.set_resample_spline_interpolation_order(1)
settings.set_resample_padding(-1000)


class BasePredictionService:
    """
    Base class for prediction services that handle medical imaging data.
    Provides common functionality for DICOM processing, image reorientation,
    and prediction output formatting.
    """
    
    # ==================== CLASS ATTRIBUTES ====================
    models: dict[str, Any] = {}
    is_initialized: bool = False
    model_info: dict[str, Any] = {}
    supported_output_modes: list[str] = []
    
    # ==================== MODEL MANAGEMENT ====================
    
    @classmethod
    def load_model_info(cls):
        """Load model information from model_info.json"""
        try:
            model_info_path = os.path.join("data", "model_info.json")
            
            if os.path.exists(model_info_path):
                with open(model_info_path, 'r') as f:
                    cls.model_info = json.load(f)
                    cls.supported_output_modes = cls.model_info.get("supportedOutputModes", [])
                    print(f"Loaded model info for {cls.model_info.get('modelName', 'Unknown')} v{cls.model_info.get('version', 'Unknown')}")
                    print(f"Supported output modes: {cls.supported_output_modes}")
            else:
                print(f"Warning: model_info.json not found at {model_info_path}")
                cls._set_fallback_model_info()
                
        except Exception as e:
            print(f"Error loading model_info.json: {str(e)}")
            cls._set_fallback_model_info()
    
    @classmethod
    def _set_fallback_model_info(cls):
        """Set fallback model info when loading fails"""
        cls.supported_output_modes = ["HTML", "JSON"]
        cls.model_info = {
            "modelName": "Unknown",
            "version": "Unknown",
            "supportedOutputModes": cls.supported_output_modes
        }

    @classmethod
    def get_model_info(cls) -> Dict[str, Any]:
        """Get model information"""
        if not cls.model_info:
            cls.load_model_info()
        return cls.model_info
    
    @classmethod
    def get_supported_output_modes(cls) -> List[str]:
        """Get list of supported output modes for this model"""
        if not cls.supported_output_modes:
            cls.load_model_info()
        return cls.supported_output_modes
    
    def load_model(self, model_weights_path: str):
        """
        Abstract method that must be implemented by child classes
        """
        raise NotImplementedError("Method load_model must be implemented in the custom logic class")
    
    def stop_model(self):
        """
        Abstract method that must be implemented by child classes
        """
        pass
    
    @classmethod
    def inference(cls, model_input, model_key: str):
        """Perform model inference with proper memory management"""
        import torch
        try:
            outputs = cls.models[model_key](model_input)
            
            # Move outputs to CPU and clear GPU memory
            if hasattr(outputs, 'detach'):  # Single output
                outputs = outputs.detach().cpu()
            elif isinstance(outputs, (list, tuple)):  # Multiple outputs
                outputs = [out.detach().cpu() for out in outputs]
            
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
                del model_input
            
            return outputs
        except Exception as e:
            print(f"Error during inference: {str(e)}")
            raise
    
    # ==================== IMAGE PROCESSING & REORIENTATION ====================
    
    def reorient_image(self, input_image: nib.Nifti1Image) -> nib.Nifti1Image:
        """
        Change the orientation of the Image data in order to be in LAS space
        x will represent the coronal plane, y the sagittal and z the axial plane.
        x increases from Right (R) to Left (L), y from Posterior (P) to Anterior (A) 
        and z from Inferior (I) to Superior (S)

        Args:
            input_image: nibabel image to reorient

        Returns:
            The output image in nibabel form
        """
        image = ImageVolume(input_image)

        # Handle 4D and 3D images differently
        if image.nifti_data.squeeze().ndim == 4:
            new_image = self._reorient(image)
        elif image.nifti_data.squeeze().ndim == 3 or image.nifti_data.ndim == 3 or image.nifti_data.squeeze().ndim == 2:
            new_image = self._reorient(image)
        else:
            raise Exception('Only 3d and 4d images are supported')

        # Create new affine matrix
        affine = self._create_reoriented_affine(image)
        
        # Create new nifti image
        if new_image.ndim > 3:  # do not squeeze single slice data
            new_image = new_image.squeeze()
        
        output = nib.nifti1.Nifti1Image(new_image, affine)
        output.header.set_slope_inter(1, 0)
        output.header.set_xyzt_units(2)  # set units for xyz (leave t as unknown)
        return output
    
    def _reorient(self, image: ImageVolume) -> numpy.ndarray:
        """
        Reorganize the data for a 3d nifti
        
        Args:
            image: ImageVolume object containing the image data
            
        Returns:
            numpy.ndarray: Reoriented image data
        """
        # Create new array where x,y,z correspond to LR (sagittal), PA (coronal), IS (axial) directions
        new_image = numpy.moveaxis(
            image.nifti_data,
            [image.sagittal_orientation.normal_component,
             image.coronal_orientation.normal_component,
             image.axial_orientation.normal_component],
            [0, 1, 2]
        )
        
        # Apply flips based on orientation
        if not image.axial_orientation.x_inverted:
            new_image = numpy.flip(new_image, axis=0)
        if image.axial_orientation.y_inverted:
            new_image = numpy.flip(new_image, axis=1)
        if image.sagittal_orientation.y_inverted:
            new_image = numpy.flip(new_image, axis=2)

        return new_image
    
    def _create_reoriented_affine(self, image: ImageVolume) -> numpy.ndarray:
        """Create new affine matrix for reoriented image"""
        affine = image.affine
        new_affine = numpy.eye(4)
        
        # Set the orientation columns
        new_affine[:, 0] = affine[:, image.sagittal_orientation.normal_component]
        new_affine[:, 1] = affine[:, image.coronal_orientation.normal_component]
        new_affine[:, 2] = affine[:, image.axial_orientation.normal_component]
        
        # Calculate origin point
        point = [0, 0, 0, 1]
        
        # Handle coordinate inversions
        if not image.axial_orientation.x_inverted:
            new_affine[:, 0] = -new_affine[:, 0]
            point[image.sagittal_orientation.normal_component] = (
                image.dimensions[image.sagittal_orientation.normal_component] - 1
            )
        
        if image.axial_orientation.y_inverted:
            new_affine[:, 1] = -new_affine[:, 1]
            point[image.coronal_orientation.normal_component] = (
                image.dimensions[image.coronal_orientation.normal_component] - 1
            )
        
        if image.coronal_orientation.y_inverted:
            new_affine[:, 2] = -new_affine[:, 2]
            point[image.axial_orientation.normal_component] = (
                image.dimensions[image.axial_orientation.normal_component] - 1
            )

        new_affine[:, 3] = numpy.dot(affine, point)
        return new_affine
    
    # ==================== DICOM PROCESSING ====================
    
    def get_series_from_seriesInstanceImages(self, seriesInstanceImages: Dict[str, Dict[str, str]]) -> Tuple[List[List[pydicom.Dataset]], List[nib.Nifti1Image]]:
        """
        Given a seriesInstanceImages dict, returns a list of sorted lists of pydicom Dataset objects 
        and nifti images, one per series.
        
        Args:
            seriesInstanceImages: Dictionary containing base64 encoded DICOM instances
            
        Returns:
            Tuple containing:
                - List of lists of pydicom Dataset objects (one list per series)
                - List of nibabel Nifti1Image objects (one per series)
        """
        all_series_pydicom = self._extract_pydicom_series(seriesInstanceImages)
        all_series_nifti = self._convert_series_to_nifti(all_series_pydicom)
        return all_series_pydicom, all_series_nifti
    
    def _extract_pydicom_series(self, seriesInstanceImages: Dict[str, Dict[str, str]]) -> List[List[pydicom.Dataset]]:
        """Extract and sort pydicom datasets from base64 encoded instances"""
        all_series_pydicom = []
        
        for _, series in seriesInstanceImages.items():
            # Decode base64 instances
            dicoms = [base64.b64decode(instance) for _, instance in series.items()]
            
            # Create pydicom datasets
            instances = [pydicom.dcmread(BytesIO(instance), force=True) for instance in dicoms]
            
            # Sort instances by InstanceNumber
            instances.sort(key=lambda x: x.InstanceNumber)
            all_series_pydicom.append(instances)
        
        return all_series_pydicom
    
    def _convert_series_to_nifti(self, all_series_pydicom: List[List[pydicom.Dataset]]) -> List[nib.Nifti1Image]:
        """Convert pydicom series to nifti images"""
        all_series_nifti = []
        
        for series in all_series_pydicom:
            scan = dicom2nifti.convert_dicom.dicom_array_to_nifti(series, None, reorient_nifti=False)
            all_series_nifti.append(self.reorient_image(scan['NII']))
        
        return all_series_nifti
    
    # ==================== LABELMAP PROCESSING ====================
    
    def reorient_labelmap(self, pred: np.ndarray, serie: List[pydicom.Dataset]) -> np.ndarray:
        """
        Reorient labelmap predictions based on DICOM series orientation for OHIF display.
        
        Args:
            pred: Nibabel image containing the segmentation predictions
            serie: List of pydicom Dataset objects from the series
            
        Returns:
            numpy.ndarray: Reoriented labelmap data
        """
        first_instance = serie[0]
        second_instance = serie[1]

        orientation = first_instance.ImageOrientationPatient
        orientation_type = self._get_orientation_type(orientation)
        print(f"Orientation: {orientation_type}")
        
        # Apply orientation-specific transformations
        labelmap_data = self._apply_orientation_transform(pred, orientation_type)
        
        # Apply anterior-posterior flip if needed
        labelmap_data = self._apply_ap_flip_if_needed(labelmap_data, orientation, orientation_type)
        
        # Handle axial slice ordering
        if (orientation_type == 'AXIAL' and 
            first_instance.ImagePositionPatient[2] > second_instance.ImagePositionPatient[2]):
            print("Flipping axial")
            labelmap_data = np.flipud(labelmap_data)
        
        # TODO: Add other orientations
        return labelmap_data
    
    def _apply_orientation_transform(self, data: np.ndarray, orientation_type: str) -> np.ndarray:
        """Apply orientation-specific transformations to data"""
        if orientation_type == 'AXIAL':
            return np.fliplr(data.transpose(2, 1, 0))
        elif orientation_type == 'SAGITTAL':
            return np.flip(data.transpose(0, 2, 1), axis=(1, 2))
        elif orientation_type == 'CORONAL':
            return np.flip(data.transpose(1, 2, 0), axis=(0, 1))
        else:
            # Fallback to axial-like processing
            return np.fliplr(data.transpose(2, 1, 0))
    
    def _apply_ap_flip_if_needed(self, labelmap_data: np.ndarray, orientation: List[float], orientation_type: str) -> np.ndarray:
        """Apply anterior-posterior flip if needed based on orientation"""
        needs_ap_flip = self._needs_anterior_posterior_flip(orientation)
        
        if needs_ap_flip:
            print("Applying anterior-posterior flip")
            if orientation_type == 'AXIAL':
                labelmap_data = np.flip(labelmap_data, axis=1)
            elif orientation_type == 'SAGITTAL':
                labelmap_data = np.flip(labelmap_data, axis=0)
            elif orientation_type == 'CORONAL':
                labelmap_data = np.flip(labelmap_data, axis=2)
        
        return labelmap_data

    def encode_labelmap_and_segments(self, labelmap_data: np.ndarray, class_mapping: Dict[str, int]) -> Tuple[str, Dict[str, int]]:
        """
        Encode labelmap data to base64 and create segments dictionary.
        
        Args:
            labelmap_data: Numpy array containing the labelmap data
            class_mapping: Dictionary mapping class names to integer labels
            
        Returns:
            Tuple containing:
                - Base64 encoded labelmap data as string
                - Dictionary of segments present in the data
        """
        labelmap_uint8 = labelmap_data.astype(np.uint8)
        encoded_data = base64.b64encode(labelmap_uint8.tobytes()).decode('utf-8')
        
        # Create segments dictionary only for segments that are present in the data
        unique_values = np.unique(labelmap_uint8)
        segments = {name: i for name, i in class_mapping.items() if i in unique_values}
        return encoded_data, segments
    
    # ==================== ORIENTATION UTILITIES ====================
    
    def _needs_anterior_posterior_flip(self, orientation: List[float]) -> bool:
        """
        Check if the image needs to be flipped based on anterior-posterior orientation.
        
        Args:
            orientation: ImageOrientationPatient values [row_x, row_y, row_z, col_x, col_y, col_z]
            
        Returns:
            bool: True if image needs to be flipped along the anterior-posterior axis
        """
        # TODO: Should generalize this to handle all orientations
        # Extract column direction cosines (indices 3, 4, 5)
        col_x, col_y, col_z = orientation[3], orientation[4], orientation[5]
        
        # Check the Y component of the column direction
        # Standard orientation has col_y = 1 (posterior to anterior)
        # If col_y = -1, it means anterior to posterior, so we need to flip
        return col_y < 0

    def _get_orientation_type(self, orientation: List[float]) -> str:
        """
        Determine image orientation based on ImageOrientationPatient
        
        Args:
            orientation: List of 6 floats representing ImageOrientationPatient
            
        Returns:
            str: 'AXIAL', 'SAGITTAL', 'CORONAL', or 'UNKNOWN'
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
    
    # ==================== PREDICTION HANDLING ====================
    
    async def predict(self, request: PredictRequest):
        """Main prediction method that handles different output modes"""
        # Ensure model info is loaded
        if not self.__class__.supported_output_modes:
            self.__class__.load_model_info()
        
        output_mode = request.outputMode
        supported_modes = self.__class__.get_supported_output_modes()
        
        # Validate output mode
        if output_mode not in supported_modes:
            return False, self._handle_unsupported_output(output_mode, supported_modes)
        
        # Check if models are initialized
        if not self.__class__.is_initialized:
            return False, self._handle_uninitialized_models()
        
        # Dynamically call the appropriate handler
        handler_method_name = f"_handle_{output_mode.lower()}_output"
        
        try:
            handler = getattr(self, handler_method_name)
            result = await handler(request)
            return True, result
        except AttributeError:
            return False, self._handle_missing_handler(output_mode, handler_method_name, supported_modes)
        except NotImplementedError:
            return False, self._handle_unimplemented_mode(output_mode, supported_modes)
        except Exception as e:
            return False, self._handle_processing_error(output_mode, str(e))
    
    # ==================== OUTPUT HANDLERS ====================
    
    async def _handle_json_output(self, request: PredictRequest):
        """Handle JSON output format"""
        raise NotImplementedError("JSON output not implemented for this model")

    async def _handle_ohif_annotations_output(self, request: PredictRequest):
        """Handle OHIF annotations output format"""
        raise NotImplementedError("OHIF annotations output not implemented for this model")

    async def _handle_html_output(self, request: PredictRequest):
        """Handle HTML output format"""
        raise NotImplementedError("HTML output not implemented for this model")

    async def _handle_webapp_output(self, request: PredictRequest):
        """Handle web app output format"""
        raise NotImplementedError("Web app output not implemented for this model")

    async def _handle_pdf_output(self, request: PredictRequest):
        """Handle PDF output format"""
        raise NotImplementedError("PDF output not implemented for this model")
    
    # ==================== ERROR HANDLERS ====================
    
    def _handle_unsupported_output(self, output_mode: str, supported_modes: List[str]):
        """Handle unsupported output mode error"""
        return HTTPResponse(
            status=400,
            success=False,
            message=f"Unsupported output mode '{output_mode}'",
            error_code="UNSUPPORTED_OUTPUT_MODE",
            data={
                "requestedMode": output_mode,
                "supportedModes": supported_modes,
                "modelInfo": self.__class__.get_model_info()
            }
        ).to_response()
    
    def _handle_uninitialized_models(self):
        """Handle uninitialized models error"""
        return HTTPResponse(
            status=500,
            success=False,
            message="Models not initialized",
            error_code="MODELS_NOT_INITIALIZED"
        ).to_response()
    
    def _handle_missing_handler(self, output_mode: str, handler_method_name: str, supported_modes: List[str]):
        """Handle missing handler method error"""
        return HTTPResponse(
            status=501,
            success=False,
            message=f"Output mode '{output_mode}' is listed as supported but handler method '{handler_method_name}' not found",
            error_code="HANDLER_METHOD_NOT_FOUND",
            data={
                "requestedMode": output_mode,
                "expectedMethod": handler_method_name,
                "supportedModes": supported_modes,
                "modelInfo": self.__class__.get_model_info()
            }
        ).to_response()
    
    def _handle_unimplemented_mode(self, output_mode: str, supported_modes: List[str]):
        """Handle unimplemented output mode error"""
        return HTTPResponse(
            status=501,
            success=False,
            message=f"Output mode '{output_mode}' is listed as supported but not implemented for this model",
            error_code="OUTPUT_MODE_NOT_IMPLEMENTED",
            data={
                "requestedMode": output_mode,
                "supportedModes": supported_modes,
                "modelInfo": self.__class__.get_model_info()
            }
        ).to_response()
    
    def _handle_processing_error(self, output_mode: str, error_message: str):
        """Handle processing error"""
        return HTTPResponse(
            status=500,
            success=False,
            message=f"Error processing {output_mode} output: {error_message}",
            error_code="PROCESSING_ERROR",
            data={
                "requestedMode": output_mode,
                "modelInfo": self.__class__.get_model_info()
            }
        ).to_response()