import base64
import contextlib
import os
import socket
import subprocess
import time
from io import BytesIO

import numpy as np
import psutil
import requests
from PIL import Image
from utils.genericLogic import BasePredictionService
from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest

os.environ["TF_FORCE_GPU_ALLOW_GROWTH"] = "true"
os.environ["TF_GPU_ALLOCATOR"] = "cuda_malloc_async"


class CustomPredictionService(BasePredictionService):
    SERVER_QUALITY_URL = "http://localhost:8501/v1/models/quality_model:predict"
    SERVER_DETECTION_URL = "http://localhost:8501/v1/models/detection_model:predict"
    SERVER_DEVICE_TYPE_URL = "http://localhost:8501/v1/models/device_type_model:predict"

    DEVICE_TYPES = [
        "BIO_ICD",
        "BIO_PM",
        "BSC_CRT-P",
        "BSC_ICD",
        "BSC_PM",
        "BSC_S-ICD",
        "ELA_PM",
        "IMC_PM",
        "MED_CRT-P",
        "MED_ICD",
        "MED_ICM",
        "MED_PM",
        "SJM_CRT-P",
        "SJM_ICD",
        "SJM_PM",
        "TPS_PM",
        "VIT_PM",
    ]

    MANUFACTURER_MAP = {
        "MED": "Medtronic",
        "BIO": "Biotronik",
        "BSC": "Boston Scientific",
        "ELA": "ELA Medical",
        "IMC": "Intermedics",
        "SJM": "St. Jude Medical (Abbott)",
        "TPS": "Telectronics",
        "VIT": "Vitatron",
    }

    DEVICE_TYPE_MAP = {
        "ICD": "Implantable cardioverter-defibrillator",
        "PM": "Pacemaker",
        "CRT-P": "Cardiac resynchronization therapy pacemaker",
        "ICM": "Implantable cardiac monitor",
        "S-ICD": "Subcutaneous implantable cardioverter-defibrillator",
    }

    DEVICE_TYPE_SHORT_MAP = {
        "ICD": "ICD",
        "PM": "Pacemaker",
        "CRT-P": "CRT-P",
        "ICM": "ILR",
        "S-ICD": "Subcutaneous ICD",
    }

    def _format_device_info(self, device_type):
        """Format device information with manufacturer and type details.

        Args:
            device_type: String in format 'MFR_TYPE' (e.g., 'MED_ICD')

        Returns:
            dict: Formatted device information
        """
        mfr_code, dev_type = device_type.split("_")
        return {
            "raw_type": device_type,
            "manufacturer": self.MANUFACTURER_MAP.get(mfr_code, mfr_code),
            "type_full": self.DEVICE_TYPE_MAP.get(dev_type, dev_type),
            "type_short": self.DEVICE_TYPE_SHORT_MAP.get(dev_type, dev_type),
        }

    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            print("Models already loaded, skipping initialization")
            return

        # Check if TensorFlow model server is running on port 8501
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        server_running = sock.connect_ex(("localhost", 8501)) == 0
        sock.close()

        if not server_running:
            print("Starting TensorFlow model server...")
            subprocess.Popen(
                [
                    "tensorflow_model_server",
                    "--rest_api_port=8501",
                    "--model_config_file=/app/models/tf_models.config",
                    "--model_config_file_poll_wait_seconds=60",
                    "--per_process_gpu_memory_fraction=0.1",  # 10% GPU memory limit
                ]
            )

            # Wait for server to start (max 30 seconds)
            for _ in range(30):
                sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                if sock.connect_ex(("localhost", 8501)) == 0:
                    sock.close()
                    print("TensorFlow model server started successfully")
                    break
                sock.close()
                time.sleep(1)
            else:
                raise RuntimeError("Failed to start TensorFlow model server")

        CustomPredictionService.is_initialized = True

    def stop_model(self):
        """Stop the TensorFlow model server if it's running."""
        if not CustomPredictionService.is_initialized:
            print("Models not loaded, nothing to stop")
            return

        # Find and terminate tensorflow_model_server process
        for proc in psutil.process_iter(["pid", "name", "cmdline"]):
            try:
                if proc.info["name"] == "tensorflow_model_server" or (
                    proc.info["cmdline"] and "tensorflow_model_server" in proc.info["cmdline"][0]
                ):
                    proc.terminate()
                    proc.wait(timeout=5)  # Wait for the process to terminate
                    print("TensorFlow model server stopped successfully")
                    break
            except (psutil.NoSuchProcess, psutil.TimeoutExpired):
                print("Failed to stop TensorFlow model server gracefully, trying to kill...")
                with contextlib.suppress(psutil.NoSuchProcess):
                    proc.kill()

        CustomPredictionService.is_initialized = False

    def _get_patient_age(self, first_instance):
        """Extract patient age from DICOM metadata."""
        age_dict = first_instance.get("00101010", {})
        age_value = age_dict.get("Value", [None])[0]
        return age_value if age_value else None

    def _get_image_from_dicom(self, first_instance):
        """Extract and convert DICOM pixel data to PIL Image."""
        pixel_data = first_instance.get("7FE00010", {}).get("InlineBinary")
        if not pixel_data:
            raise ValueError("No pixel data found in the request")

        # Decode base64 to bytes
        image_bytes = base64.b64decode(pixel_data)
        image_buffer = BytesIO(image_bytes)
        return Image.open(image_buffer).convert("L")  # Convert to grayscale

    def _detect_devices(self, pixel_data):
        """Detect devices in the image using the detection model."""
        # Use the base64 encoded pixel data directly
        instances = [{"b64": self._base64_image(pixel_data)}]
        detection_result = requests.post(
            self.SERVER_DETECTION_URL, json={"instances": instances}
        ).json()

        detection_scores = detection_result["predictions"][0]["detection_scores"]
        detection_boxes = detection_result["predictions"][0]["detection_boxes"]

        # Filter by confidence threshold
        confidence_threshold = 0.95
        confident_indices = [
            i for i, score in enumerate(detection_scores) if score >= confidence_threshold
        ]

        return {
            "scores": [detection_scores[i] for i in confident_indices],
            "boxes": [detection_boxes[i] for i in confident_indices],
        }

    def _crop_detected_regions(self, image, detection_boxes):
        """Crop image regions where devices were detected."""
        cropped_images = []
        for box in detection_boxes:
            y1 = int(box[0] * image.height)
            x1 = int(box[1] * image.width)
            y2 = int(box[2] * image.height)
            x2 = int(box[3] * image.width)
            cropped_img = image.crop((x1, y1, x2, y2))
            cropped_images.append(cropped_img)
        return cropped_images

    def _get_quality_predictions(self, instances):
        """Get quality predictions for detected devices."""
        quality_result = requests.post(
            self.SERVER_QUALITY_URL, json={"instances": instances}, timeout=10
        ).json()

        return [np.dot(one_hot, [0, 1, 2, 3, 4]) for one_hot in quality_result["predictions"]]

    def _get_device_type_predictions(self, instances):
        """Get device type predictions for detected devices."""
        device_type_result = requests.post(
            self.SERVER_DEVICE_TYPE_URL, json={"instances": instances}, timeout=10
        ).json()

        device_types = []
        confidence = []
        for one_hot in device_type_result["predictions"]:
            device_types.append(self.DEVICE_TYPES[np.argmax(one_hot, axis=0)])
            confidence.append(max(one_hot))
        return device_types, confidence

    def _dicom_to_pil(self, pixel_data, rows, cols, bits_allocated, bits_stored):
        """Convert DICOM image to PIL Image with proper bit depth handling."""
        pixel_data_decoded = base64.b64decode(pixel_data)
        if bits_allocated == 16:
            img_array_flat = np.frombuffer(pixel_data_decoded, dtype=np.uint16)
        else:
            img_array_flat = np.frombuffer(pixel_data_decoded, dtype=np.uint8)
        img_array = img_array_flat.reshape((rows, cols))

        max_possible_value = (2**bits_stored) - 1

        # Normalize to 8-bit range
        if img_array.max() > 0:
            img_array = ((img_array / max_possible_value) * 255).clip(0, 255)

        img_array = img_array.astype(np.uint8)

        # Convert to PIL Image in 'L' (grayscale) mode
        return Image.fromarray(img_array, mode="L")

    def _base64_image(self, img):
        """Convert PIL Image to base64 string."""
        buffered = BytesIO()
        img.save(buffered, format="PNG")
        return base64.b64encode(buffered.getvalue()).decode("utf-8")

    def _process_device_detections(self, image):
        """Process image for device detection and get predictions."""
        detections = self._detect_devices(image)
        if not detections["boxes"]:
            return None

        cropped_images = self._crop_detected_regions(image, detections["boxes"])
        if not cropped_images:
            return None

        instances = [{"b64": self._base64_image(im)} for im in cropped_images]
        image_quality = self._get_quality_predictions(instances)
        device_types, confidence = self._get_device_type_predictions(instances)

        # Format device information with detailed mappings
        formatted_devices = [self._format_device_info(dt) for dt in device_types]

        return {
            "device_info": formatted_devices,
            "confidence": confidence,
            "image_quality": image_quality,
            "images": [self._base64_image(im) for im in cropped_images],
        }

    async def _handle_html_output(self, request: PredictRequest):
        """Main handler for processing DICOM images and generating HTML output."""
        # Get metadata from first instance
        series_metadata = next(iter(request.seriesInstanceMetadata.values()), {})
        first_instance = next(iter(series_metadata.values()), {})

        # Get patient age
        patient_age = self._get_patient_age(first_instance)

        # Get pixel data directly
        pixel_data = first_instance.get("7FE00010", {}).get("InlineBinary")
        rows = first_instance.get("00280010", {}).get("Value", [0])[0]  # Rows
        cols = first_instance.get("00280011", {}).get("Value", [0])[0]  # Columns
        bits_allocated = first_instance.get("00280100", {}).get("Value", [0])[0]  # Bits Allocated
        bits_stored = first_instance.get("00280101", {}).get("Value", [0])[0]  # Bits Stored
        im_cxr = self._dicom_to_pil(pixel_data, rows, cols, bits_allocated, bits_stored)

        results = self._process_device_detections(im_cxr)
        html_output = HTMLParser.generate_detection_results(patient_age, results)
        return {"htmlBase64": base64.b64encode(html_output.encode("utf-8")).decode("utf-8")}
