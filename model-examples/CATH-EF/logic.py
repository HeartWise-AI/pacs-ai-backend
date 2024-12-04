from typing import List, OrderedDict
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

class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        # If models are already loaded, return
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return

        workingDirectory = config.workingDirectory
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
    
    def _common_preprocessing(self, inferences: List[List[List[List[List[int]]]]], mean: List[float], std: List[float], frames: int) -> torch.Tensor:
        lvef_batch = []
        for inference in inferences:
            numpyData = np.array(inference)

            # Arrange data for model input
            data = np.repeat(numpyData, 3, axis=1)
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
            length = frames
            if f < length:
                video = np.concatenate((video, np.zeros((c, length - f, h, w), video.dtype)), axis=1)
                c, f, h, w = video.shape
            start = np.array([0])
            video = tuple(video[:, s + 1 * np.arange(length), :, :] for s in start)[0]
            lvef_batch.append(torch.as_tensor(video).unsqueeze(0))

        return torch.cat(lvef_batch, dim=0).to('cuda' if torch.cuda.is_available() else 'cpu')

    # Optionally, redefine your specific preprocessing functions to use the common one
    def _X3D_1_preprocessing(self, request: PredictRequest) -> torch.Tensor:
        mean = [93.81117248535156, 93.81117248535156, 93.81117248535156]
        std = [59.551239013671875, 59.551239013671875, 59.551239013671875]
        return self._common_preprocessing(request.inferences, mean, std, frames=48)

    def _X3D_2_preprocessing(self, inferences: List[List[List[List[List[int]]]]]) -> torch.Tensor:
        mean = [111.72716, 111.72716, 111.72716]
        std = [47.53218, 47.53218, 47.53218]
        return self._common_preprocessing(inferences, mean, std, frames=72)

    async def _handle_html_output(self, request: PredictRequest):
        X3D_1_input = self._X3D_1_preprocessing(request)
        X3D_1_outputs = self.inference(X3D_1_input, 'X3D_1')
        
        # Process vessels and collect data for the report
        vessels_data = []
        left_coronary_inferences = []
        left_coronary_indices = []
        
        for i, output in enumerate(X3D_1_outputs):
            prediction = output.detach().cpu().numpy().argmax()
            vessel_name = self._get_vessel_name(prediction)
            
            if vessel_name is not None:
                # Generate a unique series number for each vessel
                series_number = f"Series {i+1}"
                
                if prediction == 5:  # Left Coronary
                    left_coronary_inferences.append(request.inferences[i])
                    left_coronary_indices.append(len(vessels_data))
                    # Temporarily store without LVEF
                    vessels_data.append((series_number, vessel_name, None))
                else:
                    vessels_data.append((series_number, vessel_name, None))

        # Process LVEF for left coronary vessels if any
        if left_coronary_inferences:
            X3D_2_input = self._X3D_2_preprocessing(left_coronary_inferences)
            X3D_2_outputs = self.inference(X3D_2_input, 'X3D_2')
            
            # Update vessels_data with LVEF values
            for idx, output in enumerate(X3D_2_outputs):
                lvef_value = float(output.detach().cpu().numpy())
                vessel_idx = left_coronary_indices[idx]
                
                # Replace the tuple at vessel_idx with updated LVEF
                old_tuple = vessels_data[vessel_idx]
                vessels_data[vessel_idx] = (old_tuple[0], old_tuple[1], lvef_value)

        # Generate the HTML report
        patient_age = request.age
        
        try:
            # Generate the report
            html_report = generate_vessel_report(
                vessels=vessels_data,
                patient_age=patient_age,
                display=False  # Don't display in browser, just return the base64
            )
            return {"htmlBase64": html_report}
            
        except Exception as e:
            print(f"Error generating vessel report: {str(e)}")
            return {
                "error": "Failed to generate vessel report",
                "details": str(e)
            }

    async def _handle_json_output(self, request: PredictRequest):
        X3D_1_input = self._X3D_1_preprocessing(request)
        X3D_1_outputs = self.inference(X3D_1_input, 'X3D_1')
        
        # Process X3D_1 outputs
        vessel_predictions = []
        newInferences = []
        
        for i, output in enumerate(X3D_1_outputs):
            prediction = output.detach().cpu().numpy().argmax()
            vessel_name = self._get_vessel_name(prediction)
            vessel_predictions.append({
                "index": i,
                "vessel": vessel_name
            })
            if prediction == 5:  # Left Main
                newInferences.append(request.inferences[i])

        response = {
            "predictions": {
                "vessels": vessel_predictions,
            },
            "modelRecommendations": {
                "en": "Recommendation for the next model",
                "fr": "Recommandation pour le prochain modèle",
                "presentable": len(newInferences) > 0
            }
        }

        # Only perform X3D_2 inference if there are Left Coronary predictions
        if len(newInferences) > 0:
            X3D_2_input = self._X3D_2_preprocessing(newInferences)
            X3D_2_outputs = self.inference(X3D_2_input, 'X3D_2')
            
            # Process X3D_2 outputs
            lvef_predictions = []
            for i, output in enumerate(X3D_2_outputs):
                lvef_value = float(output.detach().cpu().numpy())  # Assuming this returns the LVEF value
                lvef_predictions.append({
                    "index": i,
                    "value": lvef_value
                })
                
            response["predictions"]["LVEF"] = {
                "presentable": True,
                "values": lvef_predictions
            }

        return response