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
    DEFAULT_DATASET_MEAN = [110.4954833984375, 110.4954833984375, 110.4954833984375]
    DEFAULT_DATASET_STD = [37.805782318115234, 37.805782318115234, 37.805782318115234]

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
        print(f"DeepCORO-SYNTAX loaded. CUDA: {torch.cuda.is_available()}")

    # -- post-processing -------------------------------------------------

    def _postprocess(self, outputs: dict[str, torch.Tensor]) -> dict[str, object]:
        """Return every head. Three are continuous scores, three are severity logits.

        The continuous heads are clamped at zero because a SYNTAX score cannot be
        negative, but the raw regressor is unconstrained and places uncertain
        disease-free studies slightly below zero.
        """
        result: dict[str, object] = {}
        for key, value in outputs.items():
            v = value.detach().float().squeeze()
            if v.ndim == 0:                                   # regression head
                result[key] = float(torch.clamp(v, min=0.0))
            else:                                             # severity logits
                probs = torch.softmax(v, dim=-1)
                result[key] = [float(p) for p in probs]
                result[f"{key}_argmax"] = int(torch.argmax(probs).item())
        return result

    # -- clinical formatting --------------------------------------------

    @staticmethod
    def _band(score: float) -> tuple[int, str]:
        """Severity band from the continuous score, on the bands the model reports."""
        if score < 0.5:
            return 0, "Zero"
        if score < 23.0:
            return 1, "Low (1-22)"
        if score < 33.0:
            return 2, "Intermediate (23-32)"
        return 3, "High (>=33)"

    def _format_predictions(self, preds: dict[str, object]) -> dict[str, object]:
        cm = CustomPredictionService._class_mapping
        threshold = cm["syntax_category"]["threshold_binary_ge23"]
        labels = cm["syntax_category"]["labels"]

        glob = float(preds.get("syntax", 0.0))
        band_idx, band_label = self._band(glob)
        cat_probs = preds.get("syntax_category") or []
        head_idx = preds.get("syntax_category_argmax")

        return {
            "syntax": {"value": round(glob, 1), "unit": "points",
                       "band": band_label, "bandIndex": band_idx},
            "territory": {
                "left": round(float(preds.get("syntax_left", 0.0)), 1),
                "right": round(float(preds.get("syntax_right", 0.0)), 1),
                "unit": "points",
            },
            "severityHead": {
                "class": labels[head_idx] if head_idx is not None else None,
                "probabilities": {labels[i]: round(float(p), 3)
                                  for i, p in enumerate(cat_probs)} if cat_probs else {},
            },
            "intermediateToHigh": {
                # the operating threshold is on the CONTINUOUS head and was selected once
                # on the validation partition; it is not tuned on test data
                "positive": bool(glob >= threshold),
                "threshold": threshold,
                "note": "Rule-out use. High NPV at modest PPV; a positive result is not confirmatory.",
            },
        }

    def _diagnosis_text(self, preds: dict[str, object]) -> str:
        glob = round(float(preds.get("syntax", 0.0)), 1)
        _, band = self._band(float(preds.get("syntax", 0.0)))
        left = round(float(preds.get("syntax_left", 0.0)), 1)
        right = round(float(preds.get("syntax_right", 0.0)), 1)
        return (f"DeepCORO-SYNTAX: modified SYNTAX = {glob} points ({band}) "
                f"| left {left}, right {right}")

    def _recommendations(self, preds: dict[str, object]) -> dict[str, object]:
        glob = float(preds.get("syntax", 0.0))
        threshold = CustomPredictionService._class_mapping["syntax_category"]["threshold_binary_ge23"]
        _, band = self._band(glob)
        flagged = glob >= threshold

        if flagged:
            return {
                "en": (
                    f"<strong>Estimated modified SYNTAX {glob:.1f} points ({band}).</strong> "
                    "Above the intermediate-to-high operating threshold. Confirm the score against "
                    "the images before it informs any decision, and refer to the Heart Team for "
                    "PCI-versus-CABG discussion if confirmed. This estimate is a triage aid: its "
                    "positive predictive value is modest, and it is biased downward in occlusive "
                    "and multivessel disease."
                ),
                "fr": (
                    f"<strong>Score SYNTAX modifié estimé à {glob:.1f} points ({band}).</strong> "
                    "Au-dessus du seuil intermédiaire-à-élevé. Confirmer le score sur les images "
                    "avant toute décision et référer à l'équipe cardiaque pour la discussion "
                    "ICP-versus-PAC si confirmé. Cette estimation est une aide au triage : sa "
                    "valeur prédictive positive est modeste et le score est sous-estimé en cas de "
                    "maladie occlusive ou pluritronculaire."
                ),
            }
        return {
            "en": (
                f"<strong>Estimated modified SYNTAX {glob:.1f} points ({band}).</strong> "
                "Below the intermediate-to-high operating threshold. The model's value here is "
                "rule-out: negative predictive value was 0.98 and 0.95 in the two held-out "
                "cohorts. Confirm against the images; the score is biased downward, so a "
                "borderline result deserves a closer look."
            ),
            "fr": (
                f"<strong>Score SYNTAX modifié estimé à {glob:.1f} points ({band}).</strong> "
                "Sous le seuil intermédiaire-à-élevé. L'utilité du modèle ici est l'exclusion : "
                "valeur prédictive négative de 0,98 et 0,95 dans les deux cohortes de test. "
                "Confirmer sur les images ; le score étant sous-estimé, un résultat limite mérite "
                "un examen attentif."
            ),
        }

    # -- DICOM filtering -------------------------------------------------

    def _filter_dicoms_with_metadata(
        self, dicoms: list[pydicom.Dataset], metadata: dict
    ) -> list[pydicom.Dataset]:
        """SYNTAX is a whole-study score, so BOTH territories are kept.

        This is the substantive difference from CathEF, which sees left coronary
        acquisitions only. Dropping the right coronary acquisitions here would remove
        the evidence for the right-territory head entirely.
        """
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

    def _render_html(self, preds: dict[str, object], recs: dict[str, object]) -> str:
        cm = CustomPredictionService._class_mapping
        threshold = cm["syntax_category"]["threshold_binary_ge23"]
        glob = round(float(preds.get("syntax", 0.0)), 1)
        left = round(float(preds.get("syntax_left", 0.0)), 1)
        right = round(float(preds.get("syntax_right", 0.0)), 1)
        _, band = self._band(float(preds.get("syntax", 0.0)))
        flagged = float(preds.get("syntax", 0.0)) >= threshold
        badge_color = "#c0392b" if flagged else "#27ae60"
        badge_label = "Intermediate-to-high (>=23)" if flagged else "Below >=23 threshold"
        en = recs.get("en", "")

        html = f"""<!DOCTYPE html>
<html><head><meta charset=\"utf-8\"><title>DeepCORO-SYNTAX Report</title>
<style>
 body{{font-family:Segoe UI,Arial,sans-serif;background:#f8f9fa;color:#2c3e50;padding:24px;}}
 .card{{max-width:900px;margin:0 auto;background:#fff;border-radius:12px;padding:28px;box-shadow:0 4px 6px rgba(0,0,0,0.1);}}
 h1{{margin:0 0 8px;font-size:22px;}}
 .subtitle{{color:#7f8c8d;margin-bottom:18px;}}
 .metric{{display:flex;gap:24px;align-items:center;padding:20px;background:#f1f3f5;border-radius:8px;margin-bottom:14px;}}
 .metric .val{{font-size:48px;font-weight:700;color:#2c3e50;}}
 .metric .lbl{{font-size:14px;color:#6c757d;text-transform:uppercase;letter-spacing:1px;}}
 .terr{{display:flex;gap:16px;margin-bottom:18px;}}
 .terr div{{flex:1;background:#f1f3f5;border-radius:8px;padding:14px;text-align:center;}}
 .terr .v{{font-size:24px;font-weight:600;}}
 .badge{{display:inline-block;padding:6px 14px;border-radius:20px;color:#fff;font-weight:600;font-size:13px;background:{badge_color};}}
 .rec{{background:#eaf4ff;border-left:4px solid #3498db;padding:14px 18px;border-radius:6px;margin-bottom:14px;}}
 .warn{{background:#fff4e5;border-left:4px solid #e67e22;padding:12px 16px;border-radius:6px;font-size:13px;color:#7a4a12;}}
</style></head>
<body><div class=\"card\">
 <h1>DeepCORO-SYNTAX</h1>
 <div class=\"subtitle\">Modified SYNTAX score (16-segment) estimated from the whole angiographic study</div>
 <div class=\"metric\">
   <div><div class=\"val\">{glob}</div><div class=\"lbl\">Modified SYNTAX, points</div></div>
   <div style=\"flex:1;text-align:right;\"><span class=\"badge\">{badge_label}</span><div style=\"margin-top:8px;color:#6c757d;\">{band}</div></div>
 </div>
 <div class=\"terr\">
   <div><div class=\"v\">{left}</div><div class=\"lbl\">Left territory</div></div>
   <div><div class=\"v\">{right}</div><div class=\"lbl\">Right territory</div></div>
 </div>
 <div class=\"rec\">{en}</div>
 <div class=\"warn\"><strong>Research use only — not for clinical decision-making.</strong>
 The reference standard is a <em>modified</em> 16-segment SYNTAX score, not a core-laboratory
 score: it omits severe tortuosity and the total-occlusion sub-modifiers, so it is biased
 downward, most in occlusive and multivessel disease. The model does not localise the points it
 assigns. Manuscript under peer review; no regulatory clearance.</div>
</div></body></html>"""
        return html

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
