import base64
from typing import Any, List, OrderedDict, Dict
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest
from utils.html_parser import generate_vessel_report
import torch
import importlib
import os
import numpy as np
import torchvision
import uuid
import cv2 as cv
import pydicom
from io import BytesIO

class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        # If models are already loaded, return
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return

        workingDirectory = os.getcwd()
        model_dir = config.modelDirectory

        for key, value in config.models.items():
            # Skip if model is already loaded
            if key in self.models:
                continue

            try:
                # Get the model architecture
                architecturePath = os.path.join(workingDirectory, model_dir, value.architectureFile)
                spec = importlib.util.spec_from_file_location(key, architecturePath)
                module = importlib.util.module_from_spec(spec)
                spec.loader.exec_module(module)
                model = getattr(module, key)()

                # Load the model weights
                weightsPath = os.path.join(workingDirectory, model_dir, value.weightsFile)
                checkpoint = torch.load(weightsPath, weights_only=False, map_location='cuda' if torch.cuda.is_available() else 'cpu')
                state_dict = checkpoint['state_dict']
                new_state_dict = OrderedDict()
                if key == "X3D_1":
                    for k, v in state_dict.items():
                        name = k[17:]
                        new_state_dict[name] = v
                else:
                    for k, v in state_dict.items():
                        name = k[7:]
                        new_state_dict[name] = v
                model.load_state_dict(new_state_dict)

                # Set model to evaluation mode
                model.eval()

                # Send model to device
                model.to('cuda' if torch.cuda.is_available() else 'cpu')

                # Store the model in the class dictionary
                CustomPredictionService.models[key] = model
    
            except Exception as e:
                print(f"Error loading model {key}: {str(e)}")
                raise

        CustomPredictionService.is_initialized = True
    
    def _get_vessel_name(self, index: int) -> str:
        """
        Returns the vessel name based on the prediction index.
        
        Args:
            index (int): The prediction index from the model
            
        Returns:
            str: The corresponding vessel name
        """
        VESSEL_TYPES = {
            0: 'Aorta',
            1: 'Catheter',
            2: 'Femoral',
            3: 'Graft',
            4: 'LV',
            5: 'Left Coronary',
            6: 'Other',
            7: 'Pigtail',
            8: 'Radial',
            9: 'Right Coronary',
            10: 'Stenting'
        }
        
        return VESSEL_TYPES.get(index, "Unknown Vessel")
    
    def videoShenanigans(self, video):
        # Use uuid.uuid4() to create a unique file name
        unique_filename = f"tmp_{uuid.uuid4()}.avi"
        compressedVideo = []
        fourcc = cv.VideoWriter_fourcc("M", "J", "P", "G")
        out = cv.VideoWriter(unique_filename, fourcc, 15, video.shape[1:3])
        try:
            for i in video:
                out.write(i)
            out.release()
        except:
            print("Error in writing video file")
            # Ensure file is deleted even if an error occurs
            if os.path.exists(unique_filename):
                os.remove(unique_filename)
            # Re-raise the exception to handle it as needed by the caller
            raise

        capture = cv.VideoCapture(unique_filename)
        frame_count = int(capture.get(cv.CAP_PROP_FRAME_COUNT))
        try:
            for count in range(frame_count):
                ret, frame = capture.read()
                if not ret:
                    raise ValueError(f"Failed to load frame #{count} of video.")
                frame = cv.cvtColor(frame, cv.COLOR_BGR2RGB)
                compressedVideo.append(frame)
        finally:
            capture.release()

            # Delete the temporary file after processing
            if os.path.exists(unique_filename):
                os.remove(unique_filename)

        return np.asarray(compressedVideo).transpose(0, 3, 1, 2)
    
    def _common_preprocessing(self, series: List[pydicom.Dataset], mean: List[float], std: List[float], framesToUse: int) -> torch.Tensor:
        lvef_batch = []
        
        for serie in series:
            data = np.expand_dims(serie.pixel_array, axis=1).repeat(3, axis=1)
            compressedVideo = self.videoShenanigans(np.transpose(data, (0, 2, 3, 1)).astype(np.uint8))
            compressedVideo = compressedVideo.astype(np.float32)

            # Transform video to torch and resize
            video = torch.from_numpy(compressedVideo)
            resize_transform = torchvision.transforms.Resize((224, 224), antialias=False)
            video = resize_transform(video)

            # Apply normalization
            normalize_transform = torchvision.transforms.Normalize(mean, std)
            video = normalize_transform(video)
            video = video.permute(1, 0, 2, 3)
            video = video.numpy()

            # Set number of frames
            c, f, h, w = video.shape
            length = framesToUse
            if f < length:
                video = np.concatenate((video, np.zeros((c, length - f, h, w), video.dtype)), axis=1)
                c, f, h, w = video.shape
            start = np.array([0])
            video = tuple(video[:, s + 1 * np.arange(length), :, :] for s in start)[0]
            lvef_batch.append(torch.as_tensor(video).unsqueeze(0))

        return torch.cat(lvef_batch, dim=0).to('cuda' if torch.cuda.is_available() else 'cpu')

    # Optionally, redefine your specific preprocessing functions to use the common one
    def _X3D_1_preprocessing(self, series: List[pydicom.Dataset]) -> torch.Tensor:
        mean = [93.81117248535156, 93.81117248535156, 93.81117248535156]
        std = [59.551239013671875, 59.551239013671875, 59.551239013671875]
        return self._common_preprocessing(series, mean, std, framesToUse=48)

    def _X3D_2_preprocessing(self, series: List[pydicom.Dataset]) -> torch.Tensor:
        mean = [111.72716, 111.72716, 111.72716]
        std = [47.53218, 47.53218, 47.53218]
        return self._common_preprocessing(series, mean, std, framesToUse=72)
    
    def _get_dicoms_from_payload(self, request: Dict[str, Any]) -> List[pydicom.Dataset]:
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
        return dicoms
    
    def _generate_clinical_recommendation(self, vessel_predictions: List[Dict[str, Any]]) -> str:
        # Implement your logic here to generate a clinical recommendation
        # This is a placeholder implementation
        return "Recommendation for the next model"
    
    def _process_vessels_and_lvef(self, dicoms: List[pydicom.Dataset]):
        """Common processing for vessels and LVEF predictions"""
        X3D_1_input = self._X3D_1_preprocessing(dicoms)
        X3D_1_outputs = self.inference(X3D_1_input, 'X3D_1')
        
        # Process vessels and collect data
        vessel_predictions = []
        left_coronary_dicoms = []
        
        for i, output in enumerate(X3D_1_outputs):
            prediction = output.numpy().argmax()
            vessel_name = self._get_vessel_name(prediction)
            
            vessel_predictions.append({
                "seriesNumber": int(dicoms[i].SeriesNumber),
                "vessel": vessel_name,
                "prediction": prediction
            })
            
            # Keep only left coronary vessels for LVEF processing
            if prediction == 5:
                left_coronary_dicoms.append(dicoms[i])
        
        # Process LVEF for left coronary vessels if any
        lvef_predictions = []
        if left_coronary_dicoms:
            X3D_2_input = self._X3D_2_preprocessing(left_coronary_dicoms)
            X3D_2_outputs = self.inference(X3D_2_input, 'X3D_2')
            
            for idx, output in enumerate(X3D_2_outputs):
                lvef_value = round(float(output.numpy()), 2)
                seriesNumber = int(left_coronary_dicoms[idx].SeriesNumber)
                
                lvef_predictions.append({
                    "seriesNumber": seriesNumber,
                    "value": lvef_value
                })
        
        return vessel_predictions, lvef_predictions, left_coronary_dicoms

    def _generate_lvef_recommendation(self, lvef_predictions: List[Dict[str, Any]]) -> Dict[str, str]:
        """Generate clinical recommendation based on LVEF values"""
        if not lvef_predictions:
            return {
                "en": "No LVEF values available for assessment.",
                "fr": "Aucune valeur LVEF disponible pour l'évaluation."
            }
        
        # Find minimum LVEF value
        min_lvef = min(pred["value"] for pred in lvef_predictions)
        
        if min_lvef < 50:
            return {
                "en": f"A value under 50% was detected (minimum LVEF: {min_lvef:.1f}%), hence further cardiac evaluation and management for potential heart failure is recommended. Order an echocardiogram of the heart.",
                "fr": f"Une valeur inférieure à 50% a été détectée (LVEF minimum: {min_lvef:.1f}%), par conséquent une évaluation cardiaque supplémentaire et une gestion pour une insuffisance cardiaque potentielle est recommandée. Commandez un échocardiogramme du cœur."
            }
        else:
            return {
                "en": f"LVEF values are within normal range (minimum LVEF: {min_lvef:.1f}%). No further cardiac investigations are needed unless clinically mandated.",
                "fr": f"Les valeurs LVEF sont dans la plage normale (LVEF minimum: {min_lvef:.1f}%). Aucune investigation cardiaque supplémentaire n'est nécessaire sauf si cliniquement mandatée."
            }

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = self._get_dicoms_from_payload(request)
        # Get the age from the first instance metdata from the first series
        patient_birthdate = dicoms[0].get((0x0010, 0x0030), None)
        patient_age = None
        if patient_birthdate is not None:
            from datetime import datetime
            birthdate_str = patient_birthdate.value
            # Parse DICOM date format (YYYYMMDD)
            birthdate = datetime.strptime(birthdate_str, '%Y%m%d')
            current_date = datetime.now()
            patient_age = current_date.year - birthdate.year
            # Adjust if birthday hasn't occurred this year
            if current_date.month < birthdate.month or (current_date.month == birthdate.month and current_date.day < birthdate.day):
                patient_age -= 1

        vessel_predictions, lvef_predictions, _ = self._process_vessels_and_lvef(dicoms)
        
        # Convert to format needed for HTML report
        vessels_data = []
        for vessel_pred in vessel_predictions:
            vessel_name = vessel_pred["vessel"]
            series_number = vessel_pred["seriesNumber"]
            
            if vessel_name is not None:
                # Find corresponding LVEF value if exists
                lvef_value = None
                for lvef_pred in lvef_predictions:
                    if lvef_pred["seriesNumber"] == series_number:
                        lvef_value = lvef_pred["value"]
                        break
                
                vessels_data.append((f"Series {series_number}", vessel_name, lvef_value))

        # Generate the HTML report
        try:
            html_report = generate_vessel_report(
                vessels=vessels_data,
                patient_age=patient_age,
                display=False
            )
            return {"htmlBase64": html_report}
            
        except Exception as e:
            print(f"Error generating vessel report: {str(e)}")
            return {
                "error": "Failed to generate vessel report",
                "details": str(e)
            }

    async def _handle_json_output(self, request: PredictRequest):
        # Get the age from the first instance metdata from the first series
        dicoms = self._get_dicoms_from_payload(request)
        vessel_predictions, lvef_predictions, left_coronary_dicoms = self._process_vessels_and_lvef(dicoms)
        
        # Format vessel predictions for JSON response
        formatted_vessel_predictions = []
        for vessel_pred in vessel_predictions:
            formatted_vessel_predictions.append({
                "seriesNumber": vessel_pred["seriesNumber"],
                "vessel": vessel_pred["vessel"]
            })

        # Generate LVEF-based recommendations
        lvef_recommendations = self._generate_lvef_recommendation(lvef_predictions)

        response = {
            "predictions": {
                "vessels": formatted_vessel_predictions,
            },
            "modelRecommendations": {
                "en": lvef_recommendations["en"],
                "fr": lvef_recommendations["fr"],
                "presentable": len(left_coronary_dicoms) > 0
            }
        }

        # Add LVEF predictions if any
        if lvef_predictions:
            response["predictions"]["LVEF"] = {
                "presentable": True,
                "values": lvef_predictions
            }

        return response