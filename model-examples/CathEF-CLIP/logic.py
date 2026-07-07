import base64
import json
import os
import uuid
from io import BytesIO
from typing import Optional

import cv2 as cv
import numpy as np
import pydicom
import torch
from models.multi_instance_linear_probing import MultiInstanceLinearProbing
from models.video_encoder import VideoEncoder
from torchvision.transforms import v2
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest


SERIES_TIME_TAG = (0x0008, 0x0031)


class VideoMILWrapper(torch.nn.Module):
    def __init__(self, video_encoder, mil_model, num_videos: int):
        super().__init__()
        self.video_encoder = video_encoder
        self.mil_model = mil_model
        self.num_videos: int = num_videos

    def forward(
        self,
        x: torch.Tensor,
        video_indices: torch.Tensor | None = None,
        video_mask: torch.Tensor | None = None,
    ) -> dict[str, torch.Tensor]:
        embeddings: torch.Tensor = self.video_encoder(x)

        if embeddings.ndim == 2:
            embeddings = embeddings.unsqueeze(1)
        elif embeddings.ndim == 3 and embeddings.shape[1] > self.num_videos:
            B, NL, D = embeddings.shape
            if NL % self.num_videos != 0:
                raise ValueError(
                    f"Number of tokens (NL={NL}) is not divisible by the "
                    f"expected num_videos={self.num_videos}."
                )
            L = NL // self.num_videos
            embeddings = embeddings.view(B, self.num_videos, L, D)

        if embeddings.ndim == 4:
            B, N, _, _ = embeddings.shape
        else:
            B, N, _ = embeddings.shape

        # Only the real videos are valid instances. Zero-padded slots (added to
        # reach num_videos) must be masked out; otherwise their constant encoder
        # embedding pollutes the attention/CLS pooling and collapses predictions.
        if video_mask is not None:
            attention_mask = video_mask.to(device=embeddings.device, dtype=torch.bool)
        else:
            attention_mask = torch.ones((B, N), dtype=torch.bool, device=embeddings.device)
        return self.mil_model(embeddings, mask=attention_mask)


class CustomPredictionService(BasePredictionService):
    DEFAULT_DATASET_MEAN = [96.57122802734375, 96.57122802734375, 96.57122802734375]
    DEFAULT_DATASET_STD = [44.76901626586914, 44.76901626586914, 44.76901626586914]

    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            return

        class_mapping_path = os.path.join("models", "class_mapping.json")
        with open(class_mapping_path) as fp:
            CustomPredictionService._class_mapping = json.load(fp)

        with open(os.path.join("models", "config.json")) as fp:
            CustomPredictionService.model_config = json.load(fp)

        video_encoder = VideoEncoder(**CustomPredictionService.model_config["VideoEncoder"])

        head_structure = {
            head: value["head_dim"]
            for head, value in CustomPredictionService._class_mapping.items()
        }
        mil_model = MultiInstanceLinearProbing(
            **CustomPredictionService.model_config["MultiInstanceLinearProbing"],
            head_structure=head_structure,
        )

        CustomPredictionService.models["video_mil_wrapper"] = VideoMILWrapper(
            video_encoder,
            mil_model,
            num_videos=CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"],
        )

        checkpoint_name = CustomPredictionService.model_config["ModelStateDict"]["model_path"]
        state_dict = torch.load(
            os.path.join("models", checkpoint_name),
            map_location=torch.device("cpu"),
            weights_only=True,
        )["linear_probing"]
        state_dict = {k.replace("module.", ""): v for k, v in state_dict.items()}

        CustomPredictionService.models["video_mil_wrapper"].load_state_dict(state_dict)
        CustomPredictionService.models["video_mil_wrapper"].eval()
        CustomPredictionService.models["video_mil_wrapper"].to(
            "cuda" if torch.cuda.is_available() else "cpu"
        )
        CustomPredictionService.is_initialized = True
        print(f"CathEF-CLIP loaded. CUDA: {torch.cuda.is_available()}")

    # -- post-processing -------------------------------------------------

    def _postprocess(self, outputs: dict[str, torch.Tensor]) -> dict[str, float]:
        cm = CustomPredictionService._class_mapping
        result: dict[str, float] = {}
        for key, value in outputs.items():
            task = cm.get(key, {}).get("task")
            v = value.detach().float()
            if task == "binary_classification":
                result[key] = float(torch.sigmoid(v).item())
            else:
                # Regression: LVEF %, clamp to [0, 100]
                result[key] = float(torch.clamp(v, min=0.0, max=100.0).item())
        return result

    # -- clinical formatting --------------------------------------------

    def _format_predictions(self, preds: dict[str, float]) -> dict[str, object]:
        cm = CustomPredictionService._class_mapping
        lvef_value = float(preds.get("Value", 0.0))
        reduced_ef_prob = float(preds.get("y_true_cat", 0.0))
        reduced_ef_threshold = cm["y_true_cat"]["threshold"]
        return {
            "LVEF": {
                "value": round(lvef_value, 1),
                "unit": "%",
            },
            "reducedEF": {
                "probability": round(reduced_ef_prob, 3),
                "threshold": reduced_ef_threshold,
                "diagnosis": "reduced" if reduced_ef_prob >= reduced_ef_threshold else "preserved",
            },
        }

    def _diagnosis_text(self, preds: dict[str, float]) -> str:
        lvef = round(float(preds.get("Value", 0.0)), 1)
        prob = float(preds.get("y_true_cat", 0.0))
        threshold = CustomPredictionService._class_mapping["y_true_cat"]["threshold"]
        label = "Reduced EF (<40%)" if prob >= threshold else "Preserved EF"
        return f"CathEF-CLIP: LVEF = {lvef}% | {label} (P={prob:.2f})"

    def _recommendations(self, preds: dict[str, float]) -> dict[str, object]:
        lvef = float(preds.get("Value", 0.0))
        prob = float(preds.get("y_true_cat", 0.0))
        threshold = CustomPredictionService._class_mapping["y_true_cat"]["threshold"]
        reduced = prob >= threshold or lvef < 40.0

        if reduced:
            return {
                "en": (
                    f"<strong>Reduced LVEF detected (predicted {lvef:.1f}%, P(EF<40%)={prob:.2f}).</strong> "
                    "Obtain transthoracic echocardiogram to confirm systolic dysfunction and consider "
                    "guideline-directed heart failure management."
                ),
                "fr": (
                    f"<strong>LVEF réduite détectée (prédite {lvef:.1f}%, P(FEVG<40%)={prob:.2f}).</strong> "
                    "Obtenir une échocardiographie transthoracique pour confirmer la dysfonction systolique "
                    "et envisager la prise en charge de l'insuffisance cardiaque selon les recommandations."
                ),
                "presentable": True,
            }
        if lvef < 50.0:
            return {
                "en": (
                    f"<strong>Borderline LVEF ({lvef:.1f}%).</strong> "
                    "Consider transthoracic echocardiogram for confirmation."
                ),
                "fr": (
                    f"<strong>LVEF limite ({lvef:.1f}%).</strong> "
                    "Envisager une échocardiographie transthoracique pour confirmation."
                ),
                "presentable": True,
            }
        return {
            "en": (
                f"<strong>Preserved LVEF ({lvef:.1f}%).</strong> "
                "No further cardiac imaging required based on this prediction alone."
            ),
            "fr": (
                f"<strong>LVEF préservée ({lvef:.1f}%).</strong> "
                "Aucune imagerie cardiaque supplémentaire requise sur la seule base de cette prédiction."
            ),
            "presentable": True,
        }

    # -- DICOM filtering -------------------------------------------------

    def _filter_dicoms_with_metadata(
        self, dicoms: list[pydicom.Dataset], metadata: dict
    ) -> list[pydicom.Dataset]:
        """CathEF operates on left coronary diagnostic frames only."""
        filtered = []
        for dicom in dicoms:
            name = str(dicom.SeriesInstanceUID)
            meta = metadata.get(name)
            if not meta:
                continue
            if meta.get("main_structure") != "Left Coronary":
                continue
            if meta.get("status") != "diagnostic":
                continue
            filtered.append(dicom)
        return filtered

    # -- HTML report -----------------------------------------------------

    def _render_html(self, preds: dict[str, float], recs: dict[str, object]) -> str:
        lvef = round(float(preds.get("Value", 0.0)), 1)
        prob = float(preds.get("y_true_cat", 0.0))
        threshold = CustomPredictionService._class_mapping["y_true_cat"]["threshold"]
        is_reduced = prob >= threshold
        badge_color = "#e74c3c" if is_reduced else "#27ae60"
        badge_label = "Reduced EF (<40%)" if is_reduced else "Preserved EF"
        en = recs.get("en", "")

        html = f"""<!DOCTYPE html>
<html><head><meta charset=\"utf-8\"><title>CathEF-CLIP Report</title>
<style>
 body{{font-family:Segoe UI,Arial,sans-serif;background:#f8f9fa;color:#2c3e50;padding:24px;}}
 .card{{max-width:900px;margin:0 auto;background:#fff;border-radius:12px;padding:28px;box-shadow:0 4px 6px rgba(0,0,0,0.1);}}
 h1{{margin:0 0 8px;font-size:22px;}}
 .subtitle{{color:#7f8c8d;margin-bottom:18px;}}
 .metric{{display:flex;gap:24px;align-items:center;padding:20px;background:#f1f3f5;border-radius:8px;margin-bottom:18px;}}
 .metric .val{{font-size:48px;font-weight:700;color:#2c3e50;}}
 .metric .lbl{{font-size:14px;color:#6c757d;text-transform:uppercase;letter-spacing:1px;}}
 .badge{{display:inline-block;padding:6px 14px;border-radius:20px;color:#fff;font-weight:600;font-size:13px;background:{badge_color};}}
 .rec{{background:#eaf4ff;border-left:4px solid #3498db;padding:14px 18px;border-radius:6px;}}
</style></head>
<body><div class=\"card\">
 <h1>CathEF-CLIP LVEF Prediction</h1>
 <div class=\"subtitle\">DeepCORO-CLIP linear-probe estimate from left coronary angiogram</div>
 <div class=\"metric\">
   <div><div class=\"val\">{lvef}%</div><div class=\"lbl\">Predicted LVEF</div></div>
   <div style=\"flex:1;text-align:right;\"><span class=\"badge\">{badge_label}</span><div style=\"margin-top:8px;color:#6c757d;\">P(EF&lt;40%) = {prob:.2f}</div></div>
 </div>
 <div class=\"rec\">{en}</div>
</div></body></html>"""
        return html

    # -- handlers --------------------------------------------------------

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = self._decode_dicoms(request)
        if request.additionalMetadata:
            filtered = self._filter_dicoms_with_metadata(dicoms, request.additionalMetadata)
            if filtered:
                dicoms = filtered

        outputs = self._run_inference(dicoms)
        if outputs is None:
            return {
                "htmlBase64": base64.b64encode(
                    b"<h1>No video could be extracted from the current DICOM series</h1>"
                ).decode("utf-8"),
            }

        preds = self._postprocess(outputs)
        recs = self._recommendations(preds)
        html = self._render_html(preds, recs)
        return {"htmlBase64": base64.b64encode(html.encode("utf-8")).decode("utf-8")}

    async def _handle_json_output(self, request: PredictRequest):
        dicoms = self._decode_dicoms(request)
        if request.additionalMetadata:
            filtered = self._filter_dicoms_with_metadata(dicoms, request.additionalMetadata)
            if filtered:
                dicoms = filtered

        outputs = self._run_inference(dicoms)
        if outputs is None:
            return {
                "diagnosis": "No video could be extracted from the current DICOM series",
                "predictions": {},
                "modelRecommendations": {
                    "en": "No video could be extracted or processed.",
                    "fr": "Aucune vidéo n'a pu être extraite ou traitée.",
                    "presentable": False,
                },
            }

        preds = self._postprocess(outputs)
        return {
            "diagnosis": self._diagnosis_text(preds),
            "predictions": self._format_predictions(preds),
            "modelRecommendations": self._recommendations(preds),
        }

    # -- DICOM -> tensor pipeline (ported from DeepCoro_CLIP generic) ---

    def _decode_dicoms(self, request: PredictRequest) -> list[pydicom.Dataset]:
        dicoms: list[pydicom.Dataset] = []
        if not request.seriesInstanceImages:
            return dicoms
        for series_number in request.seriesInstanceImages:
            for instance_number in request.seriesInstanceImages[series_number]:
                b64 = request.seriesInstanceImages[series_number][instance_number]
                if not self._is_valid_base64(b64):
                    continue
                raw = base64.b64decode(b64)
                if not self._is_valid_dicom(raw):
                    continue
                try:
                    dicoms.append(pydicom.dcmread(BytesIO(raw)))
                except Exception as e:
                    print(f"dcmread failed: {e}")
        return dicoms

    def _get_video_loading_config(self) -> tuple[int, int]:
        wrapper_cfg = CustomPredictionService.model_config.get("VideoMILWrapper", {})
        stride = wrapper_cfg.get("stride") or wrapper_cfg.get("frame_stride", 1)
        resize = wrapper_cfg.get("resize", 224)
        return int(stride), int(resize)

    def _get_dataset_normalization_stats(self) -> tuple[list[float], list[float]]:
        cfg = CustomPredictionService.model_config
        mean = cfg.get("dataset_mean") or cfg.get("VideoEncoder", {}).get("dataset_mean") or self.DEFAULT_DATASET_MEAN
        std = cfg.get("dataset_std") or cfg.get("VideoEncoder", {}).get("dataset_std") or self.DEFAULT_DATASET_STD
        return list(mean), list(std)

    def _extract_series_time(self, dicom: pydicom.Dataset) -> float:
        try:
            if SERIES_TIME_TAG in dicom:
                raw = dicom[SERIES_TIME_TAG].value
                if raw is not None:
                    return float(str(raw))
        except Exception:
            pass
        return float("inf")

    def _process_dicom_to_video(
        self, dicom: pydicom.Dataset, dicom_name: str
    ) -> Optional[np.ndarray]:
        try:
            input_path = f"{dicom_name}.avi"
            avi_path = _dicom_to_avi(dicom, input_path)
            if avi_path is None:
                return None
            stride, _ = self._get_video_loading_config()
            frame_count = 0
            frames: list[np.ndarray] = []
            capture = cv.VideoCapture(avi_path)
            try:
                while True:
                    ret, frame = capture.read()
                    if not ret:
                        break
                    if frame_count % stride == 0:
                        if frame.ndim == 3:
                            frame = cv.cvtColor(frame, cv.COLOR_BGR2RGB)
                        frames.append(frame)
                    frame_count += 1
            finally:
                capture.release()
                for p in (input_path, avi_path):
                    if os.path.exists(p):
                        try:
                            os.remove(p)
                        except Exception:
                            pass
            return np.asarray(frames)
        except Exception as e:
            print(f"process_dicom_to_video error: {e}")
            return None

    def _run_inference(
        self, dicoms: list[pydicom.Dataset]
    ) -> Optional[dict[str, torch.Tensor]]:
        try:
            videos: list[np.ndarray] = []
            max_videos = CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"]
            _, resize = self._get_video_loading_config()

            dicoms = sorted(dicoms, key=self._extract_series_time)
            stop_pt = min(len(dicoms), max_videos)

            dicom_ok = 0
            for dicom in dicoms:
                if dicom_ok >= stop_pt:
                    break
                try:
                    dicom_name = dicom.SeriesInstanceUID
                except Exception:
                    dicom_name = f"tmp_{uuid.uuid4()}"
                video = self._process_dicom_to_video(dicom, dicom_name)
                if video is None:
                    continue

                video = video.astype(np.float32)
                video_t = torch.from_numpy(video)
                if video_t.shape[-1] in [1, 3]:
                    video_t = video_t.permute(0, 3, 1, 2)

                expected_frames = CustomPredictionService.model_config["VideoEncoder"]["num_frames"]
                t = video_t.shape[0]
                if t < expected_frames:
                    last = video_t[-1:].repeat(expected_frames - t, 1, 1, 1)
                    video_t = torch.cat([video_t, last], dim=0)
                elif t > expected_frames:
                    indices = torch.linspace(0, t - 1, expected_frames).long()
                    video_t = video_t[indices]

                video_t = v2.Resize((resize, resize), antialias=True)(video_t)
                mean, std = self._get_dataset_normalization_stats()
                video_t = v2.Normalize(mean, std)(video_t)
                video_t = video_t.permute(0, 2, 3, 1).contiguous()
                videos.append(video_t.cpu().numpy())
                dicom_ok += 1

            if not videos:
                return None

            video_batch = torch.from_numpy(np.array(videos)).to(dtype=torch.float32)
            num_real_videos = video_batch.shape[0]
            if video_batch.shape[0] < max_videos:
                pad_shape = (max_videos - video_batch.shape[0],) + video_batch.shape[1:]
                video_batch = torch.cat(
                    [video_batch, torch.zeros(pad_shape, dtype=video_batch.dtype)], dim=0
                )

            device = "cuda" if torch.cuda.is_available() else "cpu"
            video_batch = video_batch.unsqueeze(0).to(device)

            # Mark only the real videos as valid; padded slots stay False.
            video_mask = torch.zeros((1, max_videos), dtype=torch.bool, device=device)
            video_mask[:, :num_real_videos] = True

            model = CustomPredictionService.models["video_mil_wrapper"]
            model.eval()
            with torch.no_grad():
                outputs = model(video_batch, video_mask=video_mask)
            return outputs

        except Exception as e:
            print(f"_run_inference failed: {e}")
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            return None


def _dicom_to_avi(dicom: pydicom.Dataset, output_path: str) -> Optional[str]:
    try:
        video = dicom.pixel_array
        if video.ndim != 3:
            print(f"Unexpected pixel_array shape: {video.shape}")
            return None
        frame_height = dicom[(0x028, 0x0011)].value
        frame_width = dicom[(0x028, 0x0010)].value
        if frame_height != video.shape[1] or frame_width != video.shape[2]:
            return None
        fps = 30.0
        if (0x08, 0x2144) in dicom:
            fps = float(dicom[(0x08, 0x2144)].value)
        photometrics = dicom.PhotometricInterpretation
        if photometrics not in ("MONOCHROME1", "MONOCHROME2", "RGB"):
            return None
        fourcc = cv.VideoWriter_fourcc("M", "J", "P", "G")
        out = cv.VideoWriter(output_path, fourcc, fps, (frame_width, frame_height))
        conv = cv.COLOR_GRAY2BGR if photometrics.startswith("MONOCHROME") else cv.COLOR_RGB2BGR
        for frame in video:
            out.write(cv.cvtColor(frame, conv))
        out.release()
        return output_path
    except Exception as e:
        print(f"_dicom_to_avi error: {e}")
        return None
