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

        # Mask out zero-padded video slots so their constant encoder embedding
        # can't pollute the attention/CLS pooling and collapse predictions. Only
        # apply the caller mask when it lines up with the actual instance count N;
        # encoder modes that collapse N (e.g. aggregated study features) fall back
        # to the MIL model's all-valid default (mask=None).
        attention_mask = None
        if video_mask is not None and video_mask.shape[-1] == N:
            attention_mask = video_mask.to(device=embeddings.device, dtype=torch.bool)
        return self.mil_model(embeddings, mask=attention_mask)


class CustomPredictionService(BasePredictionService):
    DEFAULT_DATASET_MEAN = [107.3622055053711, 107.3622055053711, 107.3622055053711]
    DEFAULT_DATASET_STD = [34.67802429199219, 34.67802429199219, 34.67802429199219]

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
        mil_config = {
            key: value
            for key, value in CustomPredictionService.model_config["MultiInstanceLinearProbing"].items()
            if not key.startswith("_")
        }
        mil_model = MultiInstanceLinearProbing(
            **mil_config,
            head_structure=head_structure,
        )

        CustomPredictionService.models["video_mil_wrapper"] = VideoMILWrapper(
            video_encoder,
            mil_model,
            num_videos=CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"],
        )

        state_dict_config = CustomPredictionService.model_config["ModelStateDict"]
        checkpoint_name = state_dict_config["model_path"]
        state_dict_key = state_dict_config.get("state_dict_key", "linear_probing")
        state_dict = torch.load(
            os.path.join("models", checkpoint_name),
            map_location=torch.device("cpu"),
            weights_only=True,
        )[state_dict_key]
        state_dict = {k.replace("module.", ""): v for k, v in state_dict.items()}

        CustomPredictionService.models["video_mil_wrapper"].load_state_dict(state_dict)
        CustomPredictionService.models["video_mil_wrapper"].eval()
        CustomPredictionService.models["video_mil_wrapper"].to(
            "cuda" if torch.cuda.is_available() else "cpu"
        )
        CustomPredictionService.is_initialized = True
        print(f"DeepCORO-MACE loaded. CUDA: {torch.cuda.is_available()}")

    # -- post-processing -------------------------------------------------

    def _postprocess(self, outputs: dict[str, torch.Tensor]) -> dict[str, float]:
        """Convert each binary endpoint logit to a probability."""
        result: dict[str, float] = {}
        for key, value in outputs.items():
            spec = CustomPredictionService._class_mapping.get(key)
            if spec is None:
                raise ValueError(f"Unknown checkpoint head: {key}")
            if spec["task"] != "binary_classification" or spec["head_dim"] != 1:
                raise ValueError(f"Unsupported DeepCORO-MACE head configuration: {key}")
            logit = value.detach().float().reshape(-1)
            if logit.numel() != 1:
                raise ValueError(f"Expected one logit for {key}, received {logit.numel()}")
            result[key] = float(torch.sigmoid(logit[0]))
        return result

    # -- clinical formatting --------------------------------------------

    @staticmethod
    def _camel_case_head(head: str) -> str:
        aliases = {
            "mace_binary": "compositeMace",
            "mace_urgent_revascularization_binary": "urgentRevascularization",
            "mace_non_fatal_mi_binary": "nonFatalMyocardialInfarction",
            "complete_occlusion_coronary_disease_binary": "completeCoronaryOcclusion",
            "mace_cv_death_binary": "cardiovascularDeath",
            "mace_non_fatal_stroke_binary": "nonFatalStroke",
            "mace_heart_failure_hosp_binary": "heartFailureHospitalization",
            "mace_life_threatening_arrhythmia_binary": "lifeThreateningArrhythmia",
            "mace_cardiogenic_shock_binary": "cardiogenicShock",
        }
        return aliases[head]

    def _endpoint_payload(self, head: str, probability: float) -> dict[str, object]:
        spec = CustomPredictionService._class_mapping[head]
        threshold = float(spec["threshold"])
        payload: dict[str, object] = {
            "name": spec["name"],
            "probability": round(float(probability), 3),
            "threshold": threshold,
            "aboveResearchThreshold": bool(probability >= threshold),
        }
        if spec.get("warning"):
            payload["warning"] = spec["warning"]
        return payload

    def _format_predictions(self, preds: dict[str, float]) -> dict[str, object]:
        grouped: dict[str, dict[str, object]] = {"primary": {}, "exploratory": {}}
        for head, spec in CustomPredictionService._class_mapping.items():
            category = spec["category"]
            grouped[category][self._camel_case_head(head)] = self._endpoint_payload(
                head, preds[head]
            )
        return {
            "timeHorizon": "1 year",
            **grouped,
            "thresholdNote": (
                "The 0.5 cutoffs are uncalibrated research thresholds. Probabilities and "
                "threshold flags are not validated clinical risk estimates or diagnoses."
            ),
        }

    def _diagnosis_text(self, preds: dict[str, float]) -> str:
        composite = preds["mace_binary"]
        return (
            "DeepCORO-MACE: one-year composite MACE research score = "
            f"{composite:.3f} | not a clinical diagnosis"
        )

    def _recommendations(self, preds: dict[str, float]) -> dict[str, object]:
        composite = preds["mace_binary"]
        return {
            "en": (
                f"<strong>Composite one-year MACE research score: {composite:.3f}.</strong> "
                "Use for research demonstration only. This output is derived from coronary "
                "angiography alone and must not change monitoring, treatment, or discharge "
                "decisions. Assess the patient with validated clinical tools and clinician review."
            ),
            "fr": (
                f"<strong>Score de recherche du MACE composite à un an : {composite:.3f}.</strong> "
                "Utiliser uniquement à des fins de démonstration en recherche. Ce résultat est "
                "dérivé de la coronarographie seule et ne doit pas modifier la surveillance, le "
                "traitement ou le congé. Évaluer le patient avec des outils cliniques validés et "
                "une révision médicale."
            ),
            "presentable": True,
        }

    # -- DICOM filtering -------------------------------------------------

    def _filter_dicoms_with_metadata(
        self, dicoms: list[pydicom.Dataset], metadata: dict
    ) -> list[pydicom.Dataset]:
        """Keep diagnostic acquisitions from both coronary territories."""
        filtered = []
        for dicom in dicoms:
            meta = metadata.get(str(dicom.SeriesInstanceUID))
            if not meta:
                continue
            if meta.get("status") != "diagnostic":
                continue
            if meta.get("main_structure") not in ("Left Coronary", "Right Coronary"):
                continue
            filtered.append(dicom)
        return filtered

    # -- HTML report -----------------------------------------------------

    def _render_html(self, preds: dict[str, float], recs: dict[str, object]) -> str:
        primary_rows: list[str] = []
        exploratory_rows: list[str] = []
        for head, spec in CustomPredictionService._class_mapping.items():
            probability = preds[head]
            flag = probability >= float(spec["threshold"])
            row = (
                f"<tr><td>{spec['name']}</td><td>{probability:.3f}</td>"
                f"<td>{'Yes' if flag else 'No'}</td></tr>"
            )
            if spec["category"] == "primary":
                primary_rows.append(row)
            else:
                exploratory_rows.append(row)

        composite = preds["mace_binary"]
        en = recs.get("en", "")
        return f"""<!DOCTYPE html>
<html><head><meta charset=\"utf-8\"><title>DeepCORO-MACE Report</title>
<style>
 body{{font-family:Segoe UI,Arial,sans-serif;background:#f8f9fa;color:#23303f;padding:24px;}}
 .card{{max-width:900px;margin:0 auto;background:#fff;border-radius:12px;padding:28px;box-shadow:0 4px 12px rgba(0,0,0,.09);}}
 h1{{margin:0 0 6px;font-size:24px}} h2{{font-size:17px;margin:24px 0 8px}}
 .subtitle{{color:#687482;margin-bottom:18px}} .score{{background:#eef5ff;border-radius:8px;padding:18px;margin-bottom:16px}}
 .score strong{{font-size:40px;display:block}} table{{width:100%;border-collapse:collapse}}
 th,td{{padding:9px;border-bottom:1px solid #e3e7eb;text-align:left}} th{{background:#f4f6f8}}
 .rec{{background:#eaf4ff;border-left:4px solid #3498db;padding:14px 18px;border-radius:6px;margin-top:20px}}
 .warn{{background:#fff4e5;border-left:4px solid #e67e22;padding:14px 18px;border-radius:6px;margin-top:16px}}
</style></head><body><div class=\"card\">
 <h1>DeepCORO-MACE</h1>
 <div class=\"subtitle\">One-year cardiovascular outcome research scores from coronary angiography</div>
 <div class=\"score\"><strong>{composite:.3f}</strong>Composite MACE research score</div>
 <h2>Primary outputs</h2><table><tr><th>Endpoint</th><th>Probability</th><th>Above 0.5</th></tr>{''.join(primary_rows)}</table>
 <h2>Exploratory low-event outputs</h2><table><tr><th>Endpoint</th><th>Probability</th><th>Above 0.5</th></tr>{''.join(exploratory_rows)}</table>
 <div class=\"rec\">{en}</div>
 <div class=\"warn\"><strong>Research use only — not for clinical decision-making.</strong>
 Values are model sigmoid outputs, not calibrated clinical risks. The 0.5 flags are uncalibrated
 research thresholds. Cardiovascular death had only four events in the held-out cohort, and the
 other low-event component outputs are exploratory. No regulatory clearance.</div>
</div></body></html>"""

    # -- handlers --------------------------------------------------------

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = self._decode_dicoms(request)
        if request.additionalMetadata:
            dicoms = self._filter_dicoms_with_metadata(dicoms, request.additionalMetadata)

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
            dicoms = self._filter_dicoms_with_metadata(dicoms, request.additionalMetadata)

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
            return np.asarray(frames) if frames else None
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
                if video is None or video.size == 0:
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
        frame_height = dicom[(0x028, 0x0010)].value
        frame_width = dicom[(0x028, 0x0011)].value
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
