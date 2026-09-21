import base64
import json
import math
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
POSITIONER_PRIMARY_ANGLE_TAG = (0x0018, 0x1510)
POSITIONER_SECONDARY_ANGLE_TAG = (0x0018, 0x1511)


def classify_view(primary, secondary) -> Optional[str]:
    """Classify angiographic view from positioner angles.

    Primary angle: RAO negative, LAO positive.
    Secondary angle: Caudal negative, Cranial positive.

    Ported verbatim from DeepCORO_CLIP_DATASET/classify_angles.py so that the
    inference-time view assignment matches the ``view_class`` column used in
    training. Returns ``None`` when either angle is missing.
    """
    if primary is None or secondary is None:
        return None
    try:
        p = float(primary)
        s = float(secondary)
    except (TypeError, ValueError):
        return None
    if np.isnan(p) or np.isnan(s):
        return None

    if -110 <= p <= -70 and -15 <= s <= 15:
        return "RAO Lateral"
    if 70 <= p <= 110 and -15 <= s <= 15:
        return "LAO Lateral"
    if -45 <= p <= -15 and 15 <= s <= 45:
        return "RAO Cranial"
    if -15 <= p <= 15 and 15 <= s <= 45:
        return "AP Cranial"
    if 15 <= p <= 45 and 15 <= s <= 45:
        return "LAO Cranial"
    if -45 <= p <= -15 and -15 <= s <= 15:
        return "RAO Straight"
    if -15 <= p <= 15 and -15 <= s <= 15:
        return "AP"
    if 15 <= p <= 45 and -15 <= s <= 15:
        return "LAO Straight"
    if -45 <= p <= -15 and -45 <= s <= -15:
        return "RAO Caudal"
    if -15 <= p <= 15 and -45 <= s <= -15:
        return "AP Caudal"
    if 15 <= p <= 45 and -45 <= s <= -15:
        return "LAO Caudal"
    return "Other"


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
        view_ids: torch.Tensor | None = None,
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
        # apply the caller mask/view_ids when they line up with the actual
        # instance count N; encoder modes that collapse N (e.g. aggregated study
        # features) fall back to the MIL model's all-valid default (mask=None).
        attention_mask = None
        views = None
        if video_mask is not None and video_mask.shape[-1] == N:
            attention_mask = video_mask.to(device=embeddings.device, dtype=torch.bool)
        if view_ids is not None and view_ids.shape[-1] == N:
            views = view_ids.to(device=embeddings.device, dtype=torch.long)
        return self.mil_model(embeddings, mask=attention_mask, view_ids=views)


class CustomPredictionService(BasePredictionService):
    DEFAULT_DATASET_MEAN = [122.09012603759766, 122.09012603759766, 122.09012603759766]
    DEFAULT_DATASET_STD = [28.790834426879883, 28.790834426879883, 28.790834426879883]

    COMPONENT_HEADS = [
        "jcto_blunt_stump",
        "jcto_calcification",
        "jcto_bending_gt45",
        "jcto_occlusion_length_gt20",
    ]
    SCORE_HEAD = "jcto_score"

    # Per-artery checkpoints (v2) expose ``<head>_<artery>`` heads only. The
    # service reports the artery with the highest predicted J-CTO score.
    ARTERIES = {"lad": "LAD", "rca": "RCA", "lcx": "LCx"}
    ARTERIES_FR = {
        "lad": "l’artère interventriculaire antérieure",
        "rca": "l’artère coronaire droite",
        "lcx": "l’artère circonflexe",
    }
    ARTERY_KEY = "cto_artery_key"
    # LCx grading was validated on very few studies (20 test / 108 train CTOs);
    # the within-artery CIs span chance, so flag every LCx call.
    UNRELIABLE_ARTERIES = {"lcx"}
    LCX_WARNING_EN = (
        "Caution: LCx predictions are unreliable — the model was validated on only 20 "
        "held-out LCx CTOs (score ≥ 3 AUROC 0.56, 95% CI 0.24–0.86). Interpret with care "
        "and rely on operator review."
    )
    LCX_WARNING_FR = (
        "Attention : les prédictions pour l’artère circonflexe ne sont pas fiables — le "
        "modèle n’a été validé que sur 20 CTO circonflexes (AUROC score ≥ 3 : 0,56, IC 95 % "
        "0,24–0,86). À interpréter avec prudence et à confirmer par l’opérateur."
    )

    _GENERIC_NAMES = {
        "jcto_blunt_stump": "Blunt / flush stump",
        "jcto_calcification": "Calcification at occlusion",
        "jcto_bending_gt45": "Bending > 45°",
        "jcto_occlusion_length_gt20": "Occlusion length ≥ 20 mm",
        "jcto_score": "J-CTO score",
    }

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
        print(f"DeepCORO-CTO loaded. CUDA: {torch.cuda.is_available()}")

    # -- view handling ---------------------------------------------------

    def _view_map(self) -> dict[str, int]:
        return CustomPredictionService.model_config.get("view_labels_map", {})

    def _num_view_classes(self) -> int:
        mil_cfg = CustomPredictionService.model_config.get("MultiInstanceLinearProbing", {})
        return int(mil_cfg.get("num_view_classes", 0))

    def _view_pad_id(self) -> int:
        # MultiInstanceLinearProbing reserves index ``num_view_classes`` as PAD.
        return self._num_view_classes()

    def _dicom_view_id(self, dicom: pydicom.Dataset) -> int:
        pad_id = self._view_pad_id()
        primary = self._read_angle(dicom, POSITIONER_PRIMARY_ANGLE_TAG)
        secondary = self._read_angle(dicom, POSITIONER_SECONDARY_ANGLE_TAG)
        view = classify_view(primary, secondary)
        if view is None:
            return pad_id
        return self._view_map().get(view, pad_id)

    @staticmethod
    def _read_angle(dicom: pydicom.Dataset, tag) -> Optional[float]:
        try:
            if tag in dicom:
                raw = dicom[tag].value
                if raw is not None:
                    return float(raw)
        except Exception:
            pass
        return None

    # -- post-processing -------------------------------------------------

    def _postprocess(self, outputs: dict[str, torch.Tensor]) -> dict[str, float]:
        cm = CustomPredictionService._class_mapping
        result: dict[str, float] = {}
        for key, value in outputs.items():
            meta = cm.get(key, {})
            task = meta.get("task")
            v = value.detach().float()
            if task == "binary_classification":
                result[key] = float(torch.sigmoid(v).item())
            else:
                lo = float(meta.get("min", 0.0))
                hi = float(meta.get("max", 100.0))
                result[key] = float(torch.clamp(v, min=lo, max=hi).item())
        return self._select_artery(result)

    def _select_artery(self, preds: dict) -> dict:
        """Collapse per-artery heads onto the generic head names.

        Non-CTO arteries were trained to 0, so the artery with the highest
        predicted J-CTO score is taken as the CTO artery and its component /
        score heads are aliased onto ``COMPONENT_HEADS`` / ``SCORE_HEAD`` so the
        report code is shared with the overall (v1) checkpoints. Overall
        checkpoints (generic heads present) pass through unchanged.
        """
        if self.SCORE_HEAD in preds:
            return preds
        scores = {
            a: float(preds[f"{self.SCORE_HEAD}_{a}"])
            for a in self.ARTERIES
            if f"{self.SCORE_HEAD}_{a}" in preds
        }
        if not scores:
            return preds
        best = max(scores, key=scores.get)
        for head in self.COMPONENT_HEADS + [self.SCORE_HEAD]:
            preds[head] = float(preds.get(f"{head}_{best}", 0.0))
        preds[self.ARTERY_KEY] = best
        return preds

    # -- clinical formatting --------------------------------------------

    def _mapping_key(self, head: str, preds: Optional[dict] = None) -> str:
        """Resolve a generic head name to its class_mapping entry (per-artery aware)."""
        cm = CustomPredictionService._class_mapping
        if head in cm:
            return head
        artery = (preds or {}).get(self.ARTERY_KEY)
        if artery and f"{head}_{artery}" in cm:
            return f"{head}_{artery}"
        return head

    def _component_label(self, head: str, preds: Optional[dict] = None) -> str:
        cm = CustomPredictionService._class_mapping
        key = self._mapping_key(head, preds)
        if key in cm and cm[key].get("name"):
            name = cm[key]["name"]
            # Per-artery entries are named "<ARTERY> — <label>"; report the bare label.
            return name.split(" — ", 1)[-1] if key != head else name
        return self._GENERIC_NAMES.get(head, head)

    def _threshold(self, head: str, preds: Optional[dict] = None) -> float:
        cm = CustomPredictionService._class_mapping
        return float(cm.get(self._mapping_key(head, preds), {}).get("threshold", 0.5))

    @staticmethod
    def _score_int(score: float) -> int:
        """J-CTO score rounded to the nearest integer (half rounds up), clamped to 0-4."""
        return int(min(4, max(0, math.floor(float(score) + 0.5))))

    def _cto_artery(self, preds: dict) -> Optional[str]:
        key = preds.get(self.ARTERY_KEY)
        return self.ARTERIES.get(key) if key else None

    def _artery_unreliable(self, preds: dict) -> bool:
        return preds.get(self.ARTERY_KEY) in self.UNRELIABLE_ARTERIES

    def _format_predictions(self, preds: dict[str, float]) -> dict[str, object]:
        components = {}
        for head in self.COMPONENT_HEADS:
            prob = float(preds.get(head, 0.0))
            threshold = self._threshold(head, preds)
            components[head] = {
                "name": self._component_label(head, preds),
                "probability": round(prob, 3),
                "threshold": threshold,
                "present": bool(prob >= threshold),
            }
        score_value = float(preds.get(self.SCORE_HEAD, 0.0))
        score_int = self._score_int(score_value)
        n_above = sum(1 for c in components.values() if c["present"])
        out: dict[str, object] = {
            "jctoScore": {
                "predicted": round(score_value, 2),
                "predictedInteger": score_int,
                "componentsAboveThreshold": n_above,
                "difficulty": self._difficulty_band(score_int),
            },
            "components": components,
        }
        artery = self._cto_artery(preds)
        if artery is not None:
            out["ctoArtery"] = artery
            if self._artery_unreliable(preds):
                out["ctoArteryWarning"] = {"en": self.LCX_WARNING_EN, "fr": self.LCX_WARNING_FR}
            out["perArtery"] = {
                name: {
                    "jctoScore": round(float(preds.get(f"{self.SCORE_HEAD}_{a}", 0.0)), 2),
                    "jctoScoreInteger": self._score_int(preds.get(f"{self.SCORE_HEAD}_{a}", 0.0)),
                    "components": {
                        h: round(float(preds.get(f"{h}_{a}", 0.0)), 3) for h in self.COMPONENT_HEADS
                    },
                }
                for a, name in self.ARTERIES.items()
                if f"{self.SCORE_HEAD}_{a}" in preds
            }
        return out

    @staticmethod
    def _difficulty_band(score: float) -> str:
        if score < 1.0:
            return "easy"
        if score < 2.0:
            return "intermediate"
        if score < 3.0:
            return "difficult"
        return "very difficult"

    def _diagnosis_text(self, preds: dict[str, float]) -> str:
        raw = float(preds.get(self.SCORE_HEAD, 0.0))
        score_int = self._score_int(raw)
        band = self._difficulty_band(score_int)
        present = [
            self._component_label(h, preds)
            for h in self.COMPONENT_HEADS
            if float(preds.get(h, 0.0)) >= self._threshold(h, preds)
        ]
        comp_txt = ", ".join(present) if present else "no J-CTO components above threshold"
        artery = self._cto_artery(preds)
        artery_txt = f" [{artery}]" if artery else ""
        caution = " | CAUTION: LCx predictions unreliable" if self._artery_unreliable(preds) else ""
        return f"DeepCORO-CTO{artery_txt}: J-CTO score = {score_int} (raw {raw:.1f}, {band}) | {comp_txt}{caution}"

    def _recommendations(self, preds: dict[str, float]) -> dict[str, object]:
        score = float(preds.get(self.SCORE_HEAD, 0.0))
        score_int = self._score_int(score)
        band = self._difficulty_band(score_int)
        artery = self._cto_artery(preds)
        artery_en = f" in the {artery}" if artery else ""
        artery_fr = f" pour {self.ARTERIES_FR[preds[self.ARTERY_KEY]]}" if artery else ""
        warn_en = f" <strong>{self.LCX_WARNING_EN}</strong>" if self._artery_unreliable(preds) else ""
        warn_fr = f" <strong>{self.LCX_WARNING_FR}</strong>" if self._artery_unreliable(preds) else ""
        return {
            "en": (
                f"<strong>Predicted imaging J-CTO score {score_int}{artery_en} "
                f"(raw {score:.1f}, {band}).</strong> "
                "This is the four morphological imaging components only (range 0–4); it does not "
                "include the classic fifth point for a previously failed attempt. The score estimates "
                "the difficulty of successful guidewire crossing within 30 minutes. Higher scores "
                "indicate greater procedural complexity and may favour a hybrid or retrograde strategy "
                "and dedicated operator/lab planning. This is a research preview and must be confirmed "
                "by an operator review of the angiogram."
                f"{warn_en}"
            ),
            "fr": (
                f"<strong>Score J-CTO morphologique prédit {score_int}{artery_fr} "
                f"(brut {score:.1f}, {band}).</strong> "
                "Il s'agit des quatre composantes morphologiques d'imagerie seulement (plage 0–4); "
                "le cinquième point classique pour une tentative antérieure échouée n'est pas inclus. "
                "Le score estime la difficulté du franchissement du guide en moins de 30 minutes. "
                "Un score plus élevé indique une complexité procédurale accrue et peut orienter vers une "
                "stratégie hybride ou rétrograde. Il s'agit d'un aperçu de recherche qui doit être confirmé "
                "par la revue de l'angiogramme par l'opérateur."
                f"{warn_fr}"
            ),
            "presentable": True,
        }

    # -- HTML report -----------------------------------------------------

    def _render_html(self, preds: dict[str, float], recs: dict[str, object]) -> str:
        raw = float(preds.get(self.SCORE_HEAD, 0.0))
        score = round(raw, 1)
        score_int = self._score_int(raw)
        band = self._difficulty_band(score_int)
        artery = self._cto_artery(preds)
        badge_color = {
            "easy": "#27ae60",
            "intermediate": "#f39c12",
            "difficult": "#e67e22",
            "very difficult": "#e74c3c",
        }.get(band, "#7f8c8d")

        rows = ""
        for head in self.COMPONENT_HEADS:
            prob = float(preds.get(head, 0.0))
            present = prob >= self._threshold(head, preds)
            chip = "#e74c3c" if present else "#27ae60"
            label = "Present" if present else "Absent"
            rows += (
                f"<tr><td>{self._component_label(head, preds)}</td>"
                f"<td style='text-align:right'>{prob:.2f}</td>"
                f"<td style='text-align:center'><span style='display:inline-block;padding:3px 10px;"
                f"border-radius:12px;color:#fff;font-size:12px;background:{chip}'>{label}</span></td></tr>"
            )
        en = recs.get("en", "")

        artery_block = ""
        if artery is not None:
            artery_rows = ""
            for a, name in self.ARTERIES.items():
                key = f"{self.SCORE_HEAD}_{a}"
                if key not in preds:
                    continue
                a_raw = float(preds[key])
                mark = " <strong>(selected)</strong>" if a == preds.get(self.ARTERY_KEY) else ""
                artery_rows += (
                    f"<tr><td>{name}{mark}</td>"
                    f"<td style='text-align:right'>{self._score_int(a_raw)}</td>"
                    f"<td style='text-align:right'>{a_raw:.2f}</td></tr>"
                )
            artery_block = f"""
 <table>
   <thead><tr><th>Artery</th><th style='text-align:right'>J-CTO score</th><th style='text-align:right'>Raw</th></tr></thead>
   <tbody>{artery_rows}</tbody>
 </table>"""
        artery_lbl = f" &middot; CTO artery: {artery}" if artery else ""
        warning_block = ""
        if self._artery_unreliable(preds):
            warning_block = (
                "\n <div style=\"background:#fff4e5;border-left:4px solid #e67e22;padding:14px 18px;"
                "border-radius:6px;margin-bottom:18px;\"><strong>&#9888; "
                f"{self.LCX_WARNING_EN}</strong></div>"
            )

        html = f"""<!DOCTYPE html>
<html><head><meta charset=\"utf-8\"><title>DeepCORO-CTO Report</title>
<style>
 body{{font-family:Segoe UI,Arial,sans-serif;background:#f8f9fa;color:#2c3e50;padding:24px;}}
 .card{{max-width:900px;margin:0 auto;background:#fff;border-radius:12px;padding:28px;box-shadow:0 4px 6px rgba(0,0,0,0.1);}}
 h1{{margin:0 0 8px;font-size:22px;}}
 .subtitle{{color:#7f8c8d;margin-bottom:18px;}}
 .metric{{display:flex;gap:24px;align-items:center;padding:20px;background:#f1f3f5;border-radius:8px;margin-bottom:18px;}}
 .metric .val{{font-size:48px;font-weight:700;color:#2c3e50;}}
 .metric .lbl{{font-size:14px;color:#6c757d;text-transform:uppercase;letter-spacing:1px;}}
 .badge{{display:inline-block;padding:6px 14px;border-radius:20px;color:#fff;font-weight:600;font-size:13px;background:{badge_color};}}
 table{{width:100%;border-collapse:collapse;margin-bottom:18px;}}
 th,td{{padding:10px 12px;border-bottom:1px solid #e9ecef;font-size:14px;}}
 th{{text-align:left;color:#6c757d;text-transform:uppercase;font-size:12px;letter-spacing:1px;}}
 .rec{{background:#eaf4ff;border-left:4px solid #3498db;padding:14px 18px;border-radius:6px;}}
</style></head>
<body><div class=\"card\">
 <h1>DeepCORO-CTO J-CTO Assessment</h1>
 <div class=\"subtitle\">DeepCORO-CLIP linear-probe J-CTO scoring from coronary angiograms</div>
 <div class=\"metric\">
   <div><div class=\"val\">{score_int}</div><div class=\"lbl\">Imaging J-CTO score (0-4; no prior-failure point) &middot; raw {score}{artery_lbl}</div></div>
   <div style=\"flex:1;text-align:right;\"><span class=\"badge\">{band.title()}</span></div>
 </div>{warning_block}
 <table>
   <thead><tr><th>J-CTO component</th><th style='text-align:right'>Probability</th><th style='text-align:center'>Call</th></tr></thead>
   <tbody>{rows}</tbody>
 </table>{artery_block}
 <div class=\"rec\">{en}</div>
</div></body></html>"""
        return html

    # -- handlers --------------------------------------------------------

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = self._decode_dicoms(request)
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
            view_id_list: list[int] = []
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
                view_id_list.append(self._dicom_view_id(dicom))
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

            # View ids per slot; padded slots get the PAD view embedding index.
            pad_id = self._view_pad_id()
            view_ids = torch.full((1, max_videos), pad_id, dtype=torch.long, device=device)
            for i, vid in enumerate(view_id_list):
                view_ids[0, i] = vid

            model = CustomPredictionService.models["video_mil_wrapper"]
            model.eval()
            with torch.no_grad():
                outputs = model(video_batch, video_mask=video_mask, view_ids=view_ids)
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
