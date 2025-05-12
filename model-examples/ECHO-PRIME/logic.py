import os
import torch
import torchvision
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest
from utils.html_parser import generate_html_report
from EchoPrime import EchoPrimeInference

class CustomPredictionService(BasePredictionService):

    def load_model(self, config: Config):
        # If models are already loaded, return
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return
        try:
            workingDirectory = os.getcwd()
            self.model_dir = config.modelDirectory
            device = 'cuda' if torch.cuda.is_available() else 'cpu'
            echo_encoder_path = os.path.join(workingDirectory, self.model_dir, config.models['echo_encoder'].weightsFile)
            view_classifier_path = os.path.join(workingDirectory, self.model_dir, config.models['view_classifier'].weightsFile)
            CustomPredictionService.models['echo_encoder'] = self.load_echo_encoder(echo_encoder_path, device)
            CustomPredictionService.models['view_classifier'] = self.load_view_classifier(view_classifier_path, device)

            mil_weights_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/MIL_weights.csv")
            candidate_studies_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/candidate_studies.csv")
            candidate_embeddings_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/candidate_report_embeddings.pt")
            candidate_reports_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/candidate_reports.pkl")
            candidate_labels_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/candidate_labels.pkl")
            section_to_phenotypes_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/section_to_phenotypes.pkl")
            json_data_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/per_section.json")
            all_phrases_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/all_phr.json")
            self.roc_thresholds_path = os.path.join(workingDirectory, self.model_dir, "auxiliary_files/roc_thresholds.csv")
            self.EchoPrimeInference = EchoPrimeInference(mil_weights_path, candidate_studies_path, candidate_embeddings_path, candidate_reports_path, candidate_labels_path, section_to_phenotypes_path, json_data_path, all_phrases_path)

        except Exception as e:
            print(f"Error loading models: {str(e)}")
            raise

        CustomPredictionService.is_initialized = True
    
    def load_echo_encoder(self, model_path, device):
        """Load and initialize the echo encoder model."""
        echo_encoder = torchvision.models.video.mvit_v2_s()
        echo_encoder.head[-1] = torch.nn.Linear(echo_encoder.head[-1].in_features, 512)
        
        checkpoint = torch.load(model_path, map_location=device, weights_only=True)
        echo_encoder.load_state_dict(checkpoint)
        echo_encoder.eval()
        echo_encoder.to(device)
        
        for param in echo_encoder.parameters():
            param.requires_grad = False
        
        return echo_encoder

    def load_view_classifier(self, model_path, device):
        """Load and initialize the view classifier model."""
        vc_checkpoint = torch.load(model_path, map_location=device, weights_only=True)
        vc_state_dict = {key[6:]: value for key, value in vc_checkpoint['state_dict'].items()}
        
        view_classifier = torchvision.models.convnext_base()
        view_classifier.classifier[-1] = torch.nn.Linear(
            view_classifier.classifier[-1].in_features, 11
        )
        
        view_classifier.load_state_dict(vc_state_dict)
        view_classifier.to(device)
        view_classifier.eval()
        
        for param in view_classifier.parameters():
            param.requires_grad = False
        
        return view_classifier 
    
    async def _handle_html_output(self, request: PredictRequest):
        # Process DICOM files - handle encoded/compressed data
        if request.seriesInstanceMetadata:
            # Check if metadata contains encoded/compressed data
            for key_series, value_series in request.seriesInstanceMetadata.items():
                for key_series_instance, value_series_instance in value_series.items():
                    if '7FE00010' in value_series_instance:
                        value_meta = value_series_instance['7FE00010']
                        if isinstance(value_meta, dict) and 'InlineBinary' in value_meta:
                            binary_data = value_meta['InlineBinary']
                            if isinstance(binary_data, str) and (binary_data.startswith("gz:")):
                                # Decode and decompress if needed
                                request.seriesInstanceMetadata[key_series][key_series_instance]['7FE00010']['InlineBinary'] = self.decode_and_decompress_from_base64(binary_data)
        
        # Process DICOM files
        stack_of_videos = self.EchoPrimeInference.process_series_instance_metadata(request.seriesInstanceMetadata)
        if stack_of_videos is None:
            print("No valid DICOM files found")
            return
        
        # Encode study
        encoded_study = self.EchoPrimeInference.encode_study(
            stack_of_videos,
            CustomPredictionService.models['echo_encoder'],
            CustomPredictionService.models['view_classifier']
        )
        
        # Generate report and metrics
        report = self.EchoPrimeInference.generate_report(encoded_study)
        metrics = self.EchoPrimeInference.predict_metrics(encoded_study)
        
        # Generate HTML report
        html_report = generate_html_report(report, metrics, self.roc_thresholds_path)

        # Clean up GPU memory
        if torch.cuda.is_available():
            del stack_of_videos
            del encoded_study
            torch.cuda.empty_cache()

        return {"htmlBase64": html_report}