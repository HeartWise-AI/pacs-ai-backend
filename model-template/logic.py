from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

class CustomPredictionService(BasePredictionService):
    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return

        # TODO: Implement model loading logic here. Models should be loaded in a dictionary with the model name as the key and the value should be a PyTorch model object.
        
        CustomPredictionService.is_initialized = True
    

    async def _handle_json_output(self, request: PredictRequest):
        # TODO: Implement JSON output logic here. This method should return a dictionary with keys matching the output schema defined in the API documentation.
        pass