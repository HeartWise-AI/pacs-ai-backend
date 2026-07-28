import base64
import functools
import glob
import json
import math
import os
import re
from concurrent.futures import ThreadPoolExecutor, as_completed
from io import BytesIO
from typing import Any

import cv2
import numpy as np
import pandas as pd
import pydicom
import torch
import torchvision.utils as vutils
import utils.video_utils as video_utils
from tqdm import tqdm


def save_tensor_images(
    tensor: torch.Tensor,
    output_dir: str = "output_images",
    prefix: str = "image",
    format: str = "png",
    normalize: bool = True,
) -> None:
    """
    Save a 4D tensor of images to individual files.

    Args:
        tensor (torch.Tensor): Input tensor of shape [C, N, H, W] or [N, C, H, W]
        output_dir (str): Directory where images will be saved
        prefix (str): Prefix for the image filenames
        format (str): Image format (e.g., 'png', 'jpg')
        normalize (bool): Whether to normalize the images to [0,1] range

    Returns:
        None
    """
    # Ensure tensor is 4D
    if len(tensor.shape) != 4:
        raise ValueError(f"Expected 4D tensor, got shape {tensor.shape}")

    # Create output directory if it doesn't exist
    os.makedirs(output_dir, exist_ok=True)

    # Determine if tensor needs transposing (handling both [C,N,H,W] and [N,C,H,W] formats)
    if tensor.shape[0] == 3:  # [C,N,H,W] format
        tensor = tensor.permute(1, 0, 2, 3)

    # Iterate through the images
    for i in range(tensor.shape[0]):
        # Extract single image
        image = tensor[i]

        # Normalize if requested
        if normalize:
            image = (image - image.min()) / (image.max() - image.min())

        # Save the image
        filename = f"{prefix}_{i}.{format}"
        filepath = os.path.join(output_dir, filename)
        vutils.save_image(image, filepath)

    print(f"Saved {tensor.shape[0]} images in {output_dir}")


def save_array_to_video(frames_array, output_path, fps=30):
    # Define the codec and create VideoWriter object
    fourcc = cv2.VideoWriter_fourcc(*"mp4v")  # or 'XVID' for .avi
    out = cv2.VideoWriter(output_path, fourcc, fps, (800, 600))

    # Write each frame
    for i in range(frames_array.shape[0]):
        # Convert from RGB to BGR if necessary
        frame = cv2.cvtColor(frames_array[i], cv2.COLOR_RGB2BGR)
        out.write(frame)

    # Release the VideoWriter
    out.release()


class EchoPrimeInference:
    # Constants
    COARSE_VIEWS = [
        "A2C",
        "A3C",
        "A4C",
        "A5C",
        "Apical_Doppler",
        "Doppler_Parasternal_Long",
        "Doppler_Parasternal_Short",
        "Parasternal_Long",
        "Parasternal_Short",
        "SSN",
        "Subcostal",
    ]

    ALL_SECTIONS = [
        "Left Ventricle",
        "Resting Segmental Wall Motion Analysis",
        "Right Ventricle",
        "Left Atrium",
        "Right Atrium",
        "Atrial Septum",
        "Mitral Valve",
        "Aortic Valve",
        "Tricuspid Valve",
        "Pulmonic Valve",
        "Pericardium",
        "Aorta",
        "IVC",
        "Pulmonary Artery",
        "Pulmonary Veins",
        "Postoperative Findings",
    ]

    def __init__(
        self,
        mil_weights_path: str,
        candidate_studies_path: str,
        candidate_embeddings_path: str,
        candidate_reports_path: str,
        candidate_labels_path: str,
        section_to_phenotypes_path: str,
        json_data_path: str,
        all_phrases_path: str,
    ):
        """Initialize the inference module by loading necessary data."""
        # Load configuration files
        with open(json_data_path) as f:
            self.json_data = json.load(f)
        with open(all_phrases_path) as f:
            self.all_phrases = json.load(f)

        # Process phrases
        t_list = {
            k: [self.all_phrases[k][j] for j in self.all_phrases[k]] for k in self.all_phrases
        }
        self.phrases_per_section_list = {
            k: functools.reduce(lambda a, b: a + b, v) for (k, v) in t_list.items()
        }
        self.regex_per_section = {
            k: self._make_it_regex(v) for (k, v) in self.phrases_per_section_list.items()
        }

        # Load MIL weights per section
        mil_weights = pd.read_csv(mil_weights_path)
        self.non_empty_sections = mil_weights["Section"]
        self.section_weights = mil_weights.iloc[:, 1:].to_numpy()

        # Load candidate data
        self.candidate_studies = list(pd.read_csv(candidate_studies_path)["Study"])
        self.candidate_embeddings = torch.load(candidate_embeddings_path, weights_only=True)
        self.candidate_reports = pd.read_pickle(candidate_reports_path)
        self.candidate_labels = pd.read_pickle(candidate_labels_path)
        self.section_to_phenotypes = pd.read_pickle(section_to_phenotypes_path)
        self.MEAN = torch.tensor([29.110628, 28.076836, 29.096405]).reshape(3, 1, 1, 1)
        self.STD = torch.tensor([47.989223, 46.456997, 47.20083]).reshape(3, 1, 1, 1)
        self.DEFAULT_TOP_K = 50
        self.DEVICE = torch.device("cuda" if torch.cuda.is_available() else "cpu")
        # Move tensors to the correct device
        self.MEAN = self.MEAN.to(self.DEVICE)
        self.STD = self.STD.to(self.DEVICE)
        self.candidate_embeddings = self.candidate_embeddings.to(self.DEVICE)
        self.FRAMES_TO_TAKE = 32
        self.FRAME_STRIDE = 2
        self.VIDEO_SIZE = 224

    @staticmethod
    def _isin(phrase: str, text: str) -> bool:
        """Check if a phrase exists in text (case-insensitive)."""
        return phrase.lower() in text.lower()

    @staticmethod
    def _make_it_regex(sec: list[str]) -> re.Pattern:
        """Convert a list of section patterns into a compiled regex pattern."""
        numerical_pattern = r"(\\d+(\\.\\d+)?)"
        string_pattern = r"\\b\\w+.*?(?=\\.)"

        # Replace patterns and escape special characters
        for idx in range(len(sec)):
            sec[idx] = sec[idx].replace("(", r"\(").replace(")", r"\)").replace("+", r"\+")
            sec[idx] = re.sub(r"<numerical>", numerical_pattern, sec[idx])
            sec[idx] = re.sub(r"<string>", string_pattern, sec[idx])

        return re.compile("|".join(sec), flags=re.IGNORECASE)

    def extract_section(self, report: str, section_header: str) -> str:
        """Extract a section from the report."""
        pattern = rf"{section_header}(.*?)(?=\[SEP\])"
        match = re.search(pattern, report)
        if match:
            return f"{section_header}{match.group(1)}[SEP]"
        return "Section not found."

    def extract_features(self, report: str) -> list:
        """Extract numerical and binary features from the report."""
        features = []
        for value in self.json_data.values():
            if value["mode"] == "regression":
                match = None
                for phrase in value["label_sources"]:
                    pattern = re.compile(
                        (phrase.split("<#>")[0] + r"(\d{1,3}(?:\.\d{1,2})?)"), re.IGNORECASE
                    )
                    match = pattern.search(report)
                    if match:
                        features.append(float(match.group(1)))
                        break
                if match is None:
                    features.append(np.nan)
            elif value["mode"] == "binary":
                assigned = False
                for phrase in value["label_sources"]:
                    if self._isin(phrase, report):
                        features.append(1)
                        assigned = True
                        break
                if not assigned:
                    features.append(0)
        return features

    @staticmethod
    def _remove_subsets(strings: list[str]) -> list[str]:
        """Remove strings that are subsets of other strings in the list."""
        result = []
        for string in strings:
            if not any(string in res for res in result):
                result.append(string)
        return list(result)

    def structure_rep(self, rep: str) -> str:
        """Structure a report by organizing it into sections."""
        rep = re.sub(r"\s{2,}", " ", rep)
        structured_report = []

        for sec in self.ALL_SECTIONS:
            cur_section = self.extract_section(rep, sec)
            new_section = [sec + ":"]

            new_section.extend(
                [
                    cur_section[match.start() : match.end()]
                    for match in re.finditer(self.regex_per_section[sec], cur_section)
                ]
            )

            if len(new_section) > 1:
                new_section = self._remove_subsets(new_section)
                new_section.append("[SEP]")
                structured_report += new_section

        return " ".join(structured_report)

    def get_views(
        self,
        stack_of_videos: torch.Tensor,
        view_classifier: torch.nn.Module,
        visualize: bool = False,
    ) -> torch.Tensor:
        """Get view encodings for the videos.

        Args:
            stack_of_videos: Input video tensor
            view_classifier: The view classification model
            visualize: Whether to visualize the results

        Returns:
            One-hot encoded view classifications
        """
        stack_of_first_frames = stack_of_videos[:, :, 0, :, :].to(self.DEVICE)
        with torch.no_grad():
            out_logits = view_classifier(stack_of_first_frames)
        out_views = torch.argmax(out_logits, dim=1)
        view_list = [self.COARSE_VIEWS[v] for v in out_views]
        # Keep batch dim: squeeze() collapses N=1 from [1,11] to [11] and breaks torch.cat.
        stack_of_view_encodings = torch.nn.functional.one_hot(out_views, 11).to(self.DEVICE)

        if visualize:
            self._visualize_views(stack_of_first_frames, view_list)

        return stack_of_view_encodings

    def embed_videos(
        self, stack_of_videos: torch.Tensor, echo_encoder: torch.nn.Module
    ) -> torch.Tensor:
        """Embed videos using the EchoPrime encoder.

        Args:
            stack_of_videos: Input video tensor
            echo_encoder: The EchoPrime encoder model

        Returns:
            Video embeddings
        """
        bin_size = 50
        n_bins = math.ceil(stack_of_videos.shape[0] / bin_size)
        stack_of_features_list = []

        with torch.no_grad():
            for bin_idx in range(n_bins):
                start_idx = bin_idx * bin_size
                end_idx = min((bin_idx + 1) * bin_size, stack_of_videos.shape[0])
                bin_videos = stack_of_videos[start_idx:end_idx].to(self.DEVICE)
                bin_features = echo_encoder(bin_videos)
                stack_of_features_list.append(bin_features)

        return torch.cat(stack_of_features_list, dim=0)

    def encode_study(
        self,
        stack_of_videos: torch.Tensor,
        echo_encoder: torch.nn.Module,
        view_classifier: torch.nn.Module,
        visualize: bool = False,
    ) -> torch.Tensor:
        """Encode a complete echo study.

        Args:
            stack_of_videos: Input video tensor
            echo_encoder: The EchoPrime encoder model
            view_classifier: The view classification model
            visualize: Whether to visualize the results

        Returns:
            Study encoding
        """
        stack_of_features = self.embed_videos(stack_of_videos, echo_encoder)
        stack_of_view_encodings = self.get_views(stack_of_videos, view_classifier, visualize)
        return torch.cat((stack_of_features, stack_of_view_encodings), dim=1)

    def generate_report(self, study_embedding: torch.Tensor) -> str:
        """Generate a report from the study embedding.

        Args:
            study_embedding: The encoded study

        Returns:
            Generated report text
        """
        study_embedding = study_embedding.to(
            self.DEVICE
        )  # Ensure study embedding is on correct device
        generated_report = ""

        for s_dx, sec in enumerate(self.non_empty_sections):
            cur_weights = [
                self.section_weights[s_dx][torch.where(ten == 1)[0]]
                for ten in study_embedding[:, 512:]
            ]
            no_view_study_embedding = (
                study_embedding[:, :512]
                * torch.tensor(cur_weights, dtype=torch.float, device=self.DEVICE).unsqueeze(
                    1
                )  # Create tensor on correct device
            )
            no_view_study_embedding = torch.mean(no_view_study_embedding, dim=0)
            no_view_study_embedding = torch.nn.functional.normalize(
                no_view_study_embedding, dim=0
            )
            similarities = no_view_study_embedding @ self.candidate_embeddings.T

            extracted_section = "Section not found."
            while extracted_section == "Section not found.":
                max_id = torch.argmax(similarities)
                predicted_section = self.candidate_reports[max_id]
                extracted_section = self.extract_section(predicted_section, sec)
                if extracted_section != "Section not found.":
                    generated_report += extracted_section
                similarities[max_id] = float("-inf")

        return generated_report

    def predict_metrics(self, study_embedding: torch.Tensor) -> dict[str, float]:
        """Predict metrics from the study embedding.

        Args:
            study_embedding: The encoded study
            k: Number of top candidates to consider

        Returns:
            Dictionary of predicted metrics
        """
        study_embedding = study_embedding.to(
            self.DEVICE
        )  # Ensure study embedding is on correct device
        per_section_study_embedding = torch.zeros(
            len(self.non_empty_sections), 512, device=self.DEVICE
        )  # Create tensor on correct device

        for s_dx, _sec in enumerate(self.non_empty_sections):
            this_section_weights = [
                self.section_weights[s_dx][torch.where(view_encoding == 1)[0]]
                for view_encoding in study_embedding[:, 512:]
            ]
            this_section_study_embedding = (
                study_embedding[:, :512]
                * torch.tensor(
                    this_section_weights, dtype=torch.float, device=self.DEVICE
                ).unsqueeze(1)  # Create tensor on correct device
            )
            this_section_study_embedding = torch.sum(this_section_study_embedding, dim=0)
            per_section_study_embedding[s_dx] = this_section_study_embedding

        per_section_study_embedding = torch.nn.functional.normalize(per_section_study_embedding)
        similarities = per_section_study_embedding @ self.candidate_embeddings.T
        top_candidate_ids = torch.topk(similarities, k=self.DEFAULT_TOP_K, dim=1).indices

        preds = {}
        for s_dx, section in enumerate(self.non_empty_sections):
            for pheno in self.section_to_phenotypes[section]:
                values = [
                    self.candidate_labels[pheno][self.candidate_studies[c_ids]]
                    for c_ids in top_candidate_ids[
                        s_dx
                    ].cpu()  # Move indices to CPU for numpy operations
                    if self.candidate_studies[c_ids] in self.candidate_labels[pheno]
                ]
                if not values:
                    preds[pheno] = None
                    continue
                mean = np.nanmean(values)
                preds[pheno] = None if np.isnan(mean) else float(mean)

        return preds

    def _visualize_views(self, frames: torch.Tensor, view_list: list[str]) -> None:
        """Visualize the view classifications."""
        import cv2
        import matplotlib.pyplot as plt
        import numpy as np

        print("Preprocessed and normalized video inputs")
        rows = len(view_list) // 12 + (len(view_list) % 9 > 0)
        cols = 12
        fig, axes = plt.subplots(rows, cols, figsize=(cols, rows))
        axes = axes.flatten()

        for i in range(len(view_list)):
            display_image = (frames[i].cpu().permute([1, 2, 0]) * 255).numpy()
            display_image = np.clip(display_image, 0, 255).astype("uint8")
            display_image = np.ascontiguousarray(display_image)
            display_image = cv2.cvtColor(display_image, cv2.COLOR_RGB2BGR)
            cv2.putText(
                display_image,
                view_list[i].replace("_", " "),
                (10, 25),
                cv2.FONT_HERSHEY_SIMPLEX,
                0.7,
                (0, 220, 255),
                2,
            )
            axes[i].imshow(display_image)
            axes[i].axis("off")

        for j in range(i + 1, len(axes)):
            axes[j].axis("off")

        plt.subplots_adjust(wspace=0.05, hspace=0.05)
        plt.show()

    def load_dicom(self, dicom_path: str) -> np.ndarray:
        """Load and preprocess a DICOM file.

        Args:
            dicom_path: Path to the DICOM file

        Returns:
            Preprocessed pixel array
        """
        dcm = pydicom.dcmread(dicom_path)
        pixels = video_utils.load_pixel_array(dcm)

        # Exclude images like (600,800) or (600,800,3)
        if pixels.ndim < 3 or pixels.shape[2] == 3:
            return None

        # If single channel repeat to 3 channels
        if pixels.ndim == 3:
            pixels = np.repeat(pixels[..., None], 3, axis=3)

        # Mask everything outside ultrasound region
        return video_utils.mask_outside_ultrasound(pixels)

    def preprocess_video(self, pixels: np.ndarray) -> torch.Tensor:
        """Preprocess video frames.

        Args:
            pixels: Raw pixel array from DICOM

        Returns:
            Preprocessed and normalized tensor
        """
        x = np.zeros((len(pixels), self.VIDEO_SIZE, self.VIDEO_SIZE, 3))
        for i in range(len(x)):
            x[i] = video_utils.crop_and_scale(pixels[i])

        x = torch.as_tensor(x, dtype=torch.float).permute([3, 0, 1, 2])
        x = x.to(self.DEVICE)  # Move tensor to correct device before normalization
        # Normalize
        x.sub_(self.MEAN).div_(self.STD)

        # If not enough frames add padding
        if x.shape[1] < self.FRAMES_TO_TAKE:
            padding = torch.zeros(
                (3, self.FRAMES_TO_TAKE - x.shape[1], self.VIDEO_SIZE, self.VIDEO_SIZE),
                dtype=torch.float,
                device=self.DEVICE,  # Create padding tensor on correct device
            )
            x = torch.cat((x, padding), dim=1)

        start = 0
        return x[:, start : (start + self.FRAMES_TO_TAKE) : self.FRAME_STRIDE, :, :]

    def process_single_dicom(self, dicom_path: str) -> torch.Tensor:
        """Process a single DICOM file.

        Args:
            dicom_path: Path to the DICOM file

        Returns:
            Preprocessed video tensor or None if processing fails
        """
        try:
            pixels = self.load_dicom(dicom_path)
            if pixels is not None:
                return self.preprocess_video(pixels)
        except Exception as e:
            print(f"Error processing {dicom_path}: {str(e)}")
        return None

    def process_single_series_instance_metadata(self, instances: dict[str, Any]) -> torch.Tensor:
        """Process a single series instance metadata.

        Args:
            seriesInstanceMetadata: Series instance metadata

        Returns:
            Preprocessed video tensor or None if processing fails
        """
        pixels_array = []
        for metadata in instances.values():
            try:
                row = metadata["00280010"]["Value"][0]
                column = metadata["00280011"]["Value"][0]
                frames = metadata["00280008"]["Value"][0]
                channels = metadata["00280002"]["Value"][0]
                data = metadata["7FE00010"]["InlineBinary"]

                data = np.frombuffer(base64.b64decode(data), dtype=np.uint8)
                data = data.reshape(frames, row, column, channels)
                pixels = np.array(data)
                # pixels should be shape (frames,row, column, channels)
                # Exclude images like (600,800) or (600,800,3)
                if pixels.ndim < 3 or pixels.shape[2] == 3:
                    return None

                # If single channel repeat to 3 channels
                if pixels.ndim == 3:
                    pixels = np.repeat(pixels[..., None], 3, axis=3)

                # Mask everything outside ultrasound region
                pixels = video_utils.mask_outside_ultrasound(pixels)

                pixels_array.append(self.preprocess_video(pixels))

            except Exception as e:
                print(f"Error processing series instance metadata: {str(e)}")
                continue

        return torch.stack(pixels_array)

    def process_dicoms(self, input_dir: dict[str, Any]) -> torch.Tensor:
        """Process all DICOM files in a directory using concurrent processing.

        Args:
            input_dir: Directory containing DICOM files

        Returns:
            Stack of preprocessed videos as a tensor
        """
        dicom_paths = glob.glob(f"{input_dir}/**/*.dcm", recursive=True)
        stack_of_videos = []

        # Use 2x number of CPU cores for threads since I/O operations are involved
        max_workers = 8

        with ThreadPoolExecutor(max_workers=max_workers) as executor:
            # Submit all tasks
            future_to_path = {
                executor.submit(self.process_single_dicom, path): path for path in dicom_paths
            }

            # Process results as they complete
            for future in tqdm(
                as_completed(future_to_path),
                total=len(dicom_paths),
                desc="Processing DICOM files",
            ):
                result = future.result()
                if result is not None:
                    stack_of_videos.append(result)

        if not stack_of_videos:
            return None
        return torch.stack(stack_of_videos).to(
            self.DEVICE
        )  # Move stacked tensor to correct device

    def process_series_instance_metadata(
        self, seriesInstanceMetadata: dict[str, Any]
    ) -> torch.Tensor:
        """Process all DICOM files in a directory using concurrent processing.

        Args:
            input_dir: Directory containing DICOM files

        Returns:
            Stack of preprocessed videos as a tensor
        """
        stack_of_videos = []

        # Use 2x number of CPU cores for threads since I/O operations are involved
        max_workers = 8

        with ThreadPoolExecutor(max_workers=max_workers) as executor:
            # Submit all tasks
            future_to_path = {
                executor.submit(
                    self.process_single_series_instance_metadata, instances
                ): instances
                for _, instances in seriesInstanceMetadata.items()
            }

            # Process results as they complete
            for future in tqdm(
                as_completed(future_to_path),
                total=len(seriesInstanceMetadata),
                desc="Processing DICOM files",
            ):
                result = future.result()
                if result is not None:
                    stack_of_videos.extend(result)
                    # Clean up intermediate result tensor
                    if torch.cuda.is_available():
                        del result
                        torch.cuda.empty_cache()

        if not stack_of_videos:
            return None
        return torch.stack(stack_of_videos).to(
            self.DEVICE
        )  # Move stacked tensor to correct device

    def process_series_instance_images(
        self, seriesInstanceImages: dict[int, dict[int, str]]
    ) -> torch.Tensor:
        """Process full DICOM files received as base64-encoded images.

        Used as a temporary workaround when the caller does not support
        DICOM tag forwarding (e.g. cardio-agent).

        Args:
            seriesInstanceImages: Dict of series_number -> instance_number -> base64 DICOM

        Returns:
            Stack of preprocessed videos as a tensor, or None if no valid videos found
        """
        stack_of_videos = []

        for series_number, instances in seriesInstanceImages.items():
            for instance_number, dicom_base64 in instances.items():
                try:
                    dicom_data = base64.b64decode(dicom_base64)
                    dcm = pydicom.dcmread(BytesIO(dicom_data))

                    # Skip single-frame DICOMs
                    num_frames = getattr(dcm, 'NumberOfFrames', None)
                    if num_frames is None or int(num_frames) <= 1:
                        print(f"Skipping single-frame DICOM for series {series_number} instance {instance_number}")
                        continue

                    pixels = video_utils.load_pixel_array(dcm)

                    # Exclude images like (600,800) or (600,800,3)
                    if pixels.ndim < 3 or pixels.shape[2] == 3:
                        continue

                    # If single channel repeat to 3 channels
                    if pixels.ndim == 3:
                        pixels = np.repeat(pixels[..., None], 3, axis=3)

                    # Mask everything outside ultrasound region
                    pixels = video_utils.mask_outside_ultrasound(pixels)

                    stack_of_videos.append(self.preprocess_video(pixels))

                except Exception as e:
                    print(f"Error processing series {series_number} instance {instance_number}: {str(e)}")
                    continue

        if not stack_of_videos:
            return None
        return torch.stack(stack_of_videos).to(self.DEVICE)
