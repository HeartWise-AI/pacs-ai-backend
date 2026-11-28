"""EchoAlign inference for PACS-AI (research use only, CC BY-NC 4.0).

EchoAlign (Aly, Taratynova, Mecharbat, Sandler, Sokol, Penn, Maier-Hein, Saeed, Yaqub;
MBZUAI BioMedIA with iCardio.ai and DKFZ; arXiv:2611.27517) encodes each echo clip with a
TimeSformer, pools every clip of the study with its study transformer into 13 vectors (global
plus 12 cardiac structures), projects them to an 832-number PCA embedding and applies the 53
linear probes the authors released. No view classifier is needed.

Pipeline per request: DICOM instances -> skip non-US/TEE (and still images unless
instance_selection is "all") -> per clip 16 frames at 24 fps, ultrasound-region crop, square pad,
224 px -> clip vectors -> study outputs, plus per-clip outputs (each clip pooled alone).
"""
from __future__ import annotations

import base64
import gc
import html
import json
import os
import re
import tempfile
import time
from io import BytesIO
from typing import Any, Dict, Iterator, List, Optional, Tuple

import numpy as np
import pydicom
import torch
import yaml
from pydicom.multival import MultiValue
from scipy.special import expit, softmax

from models.echoalign import EchoAlign, load_echo
from models.echoalign.pooling import pool_studies
from utils.fast_request import decode_base64
from utils.genericLogic import BasePredictionService
from utils.html_parser import HTMLParser
from utils.http_utils import Config, PredictRequest

MODEL_KEY = "echoalign"
# Memory/runtime guard for automatic runs: a study with more usable clips is skipped with a reason.
MAX_CLIPS = int(os.environ.get("ECHOALIGN_MAX_CLIPS", "200"))
RESEARCH_BANNER = "Research use only — EchoAlign (CC BY-NC 4.0), not for clinical decisions"
ATTRIBUTION = (
    "EchoAlign by Aly, Taratynova, Mecharbat, Sandler, Sokol, Penn, Maier-Hein, Saeed and Yaqub "
    "(MBZUAI BioMedIA, iCardio.ai, DKFZ), arXiv:2611.27517, github.com/BioMedIA-MBZUAI/EchoAlign. "
    "Weights CC BY-NC 4.0."
)
KEY_HEADS = [
    "aortic_stenosis_classification", "aortic_stenosis_grade", "transaortic_peak_velocity",
    "transaortic_mean_gradient", "transaortic_peak_gradient", "aortic_valve_replacement",
    "ejection_fraction", "lv_systolic_dysfunction", "lv_systolic_dysfunction_grade",
    "aortic_regurgitation", "aortic_regurgitation_grade", "mitral_regurgitation_classification",
    "mitral_regurgitation_grade", "tricuspid_regurgitation_classification", "tricuspid_regurgitation_grade",
]
UNITS = {"transaortic_peak_velocity": "m/s", "transaortic_mean_gradient": "mmHg", "transaortic_peak_gradient": "mmHg",
         "ejection_fraction": "%", "aortic_root_dimension": "cm", "lavi": "mL/m2", "pa_pressure": "mmHg", "rvsp": "mmHg"}
# Ordinal head -> grade names key in grades.yaml.
GRADE_KEY = {
    "aortic_stenosis_grade": "aortic_stenosis", "aortic_regurgitation_grade": "aortic_regurgitation",
    "mitral_regurgitation_grade": "mitral_regurgitation", "tricuspid_regurgitation_grade": "tricuspid_regurgitation",
    "lv_systolic_dysfunction_grade": "lv_systolic_dysfunction", "la_enlargement_grade": "la_enlargement",
    "lv_diastolic_dysfunction_grade": "lv_diastolic_dysfunction", "lv_enlargement_grade": "lv_enlargement",
    "lvh_grade": "lvh", "mitral_calcification_grade": "mitral_calcification",
    "pulmonary_hypertension_grade": "pulmonary_hypertension",
}
# Report findings in PanEcho's order. Shared findings use PanEcho's diagnosis keys (so the report
# groups them like PanEcho's); EchoAlign-only outputs get descriptive keys.
REPORT_FINDINGS = [
    ("ejection_fraction", "Left ventricular (LV) ejection fraction"),
    ("lv_enlargement_grade", "LV size"),
    ("lvh_grade", "Any level of LV hypertrophy (increased wall thickness)"),
    ("lv_systolic_dysfunction_grade", "LV systolic function"),
    ("rwma", "LV wall motion abnormalities"),
    ("septal_motion_abnormality", "LV septal motion abnormality"),
    ("lv_diastolic_dysfunction_grade", "LV diastolic function"),
    ("rvsp", "Right ventricular (RV) systolic pressure"),
    ("rv_enlargement", "RV size"),
    ("rv_systolic_dysfunction", "RV systolic function"),
    ("pa_pressure", "Pulmonary artery pressure (RV afterload)"),
    ("pulmonary_hypertension_grade", "Pulmonary hypertension (RV)"),
    ("la_enlargement_grade", "Left atrial (LA) size"),
    ("lavi", "LA volume index"),
    ("ra_enlargement", "Right atrial (RA) size"),
    ("elevated_ra_pressure", "Elevated RA pressure"),
    ("ivc_dilation", "IVC dilation (RA pressure)"),
    ("bicuspid_av", "Bicuspid aortic valve"),
    ("transaortic_mean_gradient", "Aortic valve mean gradient (AI estimate)"),
    ("transaortic_peak_gradient", "Aortic valve peak gradient (AI estimate)"),
    ("aortic_sclerosis", "Aortic valve sclerosis"),
    ("aortic_valve_replacement", "Aortic valve replacement"),
    ("aortic_regurgitation_grade", "Aortic valve regurgitation"),
    ("mitral_stenosis", "Mitral valve stenosis"),
    ("mitral_regurgitation_grade", "Mitral valve regurgitation"),
    ("mitral_calcification_grade", "Mitral annular calcification (MV)"),
    ("mitral_valve_prolapse", "Mitral valve prolapse"),
    ("tricuspid_regurgitation_grade", "Tricuspid valve regurgitation"),
    ("pulmonic_regurgitation", "Pulmonic valve regurgitation"),
    ("aortic_root_dimension", "Aortic root diameter (Ao)"),
    ("ascending_aorta_dilation", "Ascending aorta dilation (Ao)"),
    ("atrial_fibrillation", "Atrial fibrillation"),
    ("pacemaker", "Pacemaker"),
]
# What a called / not-called binary head means for its finding (same wording as the viewer template).
BINARY_WORDING = {
    "rwma": ("Present", "Absent"), "septal_motion_abnormality": ("Present", "Absent"),
    "rv_enlargement": ("Enlarged", "Normal"), "rv_systolic_dysfunction": ("Reduced", "Normal"),
    "ra_enlargement": ("Enlarged", "Normal"), "elevated_ra_pressure": ("Elevated", "Not elevated"),
    "ivc_dilation": ("Dilated", "Not dilated"), "bicuspid_av": ("Bicuspid", "Not detected"),
    "aortic_sclerosis": ("Present", "Absent"), "aortic_valve_replacement": ("Prosthetic valve", "Native valve"),
    "mitral_stenosis": ("Present", "Absent"), "mitral_valve_prolapse": ("Present", "Absent"),
    "pulmonic_regurgitation": ("Present", "Absent"), "ascending_aorta_dilation": ("Dilated", "Not dilated"),
    "atrial_fibrillation": ("Present", "Absent"), "pacemaker": ("Present", "Absent"),
}
AS_SCREENING_NOTE_EN = (
    "AI screening scores from the 2D and colour clips (no Doppler spectra): not a diagnosis. EchoAlign compresses "
    "the AV peak velocity and gradients towards the middle of the range and did not call severe AS on MHI; use the "
    "measured Doppler values (Vmax, mean gradient, valve area)."
)
AS_SCREENING_NOTE_FR = (
    "Scores de d\u00e9pistage IA \u00e0 partir des clips 2D et couleur (sans spectres Doppler) : pas un diagnostic. "
    "EchoAlign resserre la vitesse de pointe aortique et les gradients vers le milieu de l'\u00e9chelle et n'a pas "
    "attribu\u00e9 de SA s\u00e9v\u00e8re \u00e0 l'ICM ; utiliser les valeurs Doppler mesur\u00e9es (Vmax, gradient "
    "moyen, surface valvulaire)."
)
ZERO_SHOT = ["aortic_stenosis", "aortic_regurgitation", "mitral_regurgitation", "tricuspid_regurgitation",
             "lv_systolic_dysfunction"]


# Same transesophageal markers as PanEcho: study/series descriptions (English and French; ETT is TTE)
# and TEE probe names in TransducerData (e.g. Philips X8-2t, X7-2t; GE 6VT-D, 9VT-D).
_TEE_DESCRIPTION = re.compile(r"\bTEE\b|\bETO\b|TRANS[- ]?O?ESOPH|TRANSESOPH|TRANSOESOPH", re.IGNORECASE)
_TEE_TRANSDUCER = re.compile(r"^(X\d+[-_]\d+T|\d+VT(-D)?|.*TEE.*)$", re.IGNORECASE)


def is_tee(ds) -> bool:
    """True when the clip's headers say it was acquired transesophageally."""
    for keyword in ("StudyDescription", "SeriesDescription", "ProtocolName"):
        value = str(getattr(ds, keyword, "") or "")
        if value and _TEE_DESCRIPTION.search(value):
            return True
    transducer = getattr(ds, "TransducerData", None)
    if transducer:
        first = transducer[0] if isinstance(transducer, (list, tuple, MultiValue)) else str(transducer).split("\\")[0]
        if _TEE_TRANSDUCER.match(str(first).strip()):
            return True
    return False


def frame_count(ds) -> int:
    try:
        return max(1, int(getattr(ds, "NumberOfFrames", 1) or 1))
    except (TypeError, ValueError):
        return 1


def select_reason(ds, selection: str) -> Optional[str]:
    """Why an instance is not used, or None when it is used."""
    if str(getattr(ds, "Modality", "")) != "US" or not hasattr(ds, "Rows"):
        return "non_image"
    if is_tee(ds):
        return "tee"
    if selection != "all" and frame_count(ds) <= 1:
        return "single_frame"
    return None


def apply_head(x: np.ndarray, name: str, meta: Dict[str, Any], heads: Dict[str, np.ndarray],
               grades: Dict[str, Any]) -> Dict[str, Any]:
    """One released linear probe on the 832-number PCA study embedding (HF model card recipe)."""
    info = meta[name]
    z = (x - heads[f"{name}/mean"]) / heads[f"{name}/scale"]
    coef, intercept = heads[f"{name}/coef"], heads[f"{name}/intercept"]
    if info["mode"] == "classification":
        p = float(expit(z @ coef + intercept))
        threshold = float(info.get("threshold", 0.5))
        return {"type": "classification", "probability": round(p, 4), "threshold": round(threshold, 4),
                "called": bool(p >= threshold)}
    if info["mode"] == "ordinal":
        logits = np.atleast_1d(z @ coef.T + intercept)
        p = softmax(logits) if len(logits) > 1 else np.array([1 - expit(logits[0]), expit(logits[0])])
        k = int(info["n_classes"])
        grade = int(np.searchsorted(np.asarray(info["cuts"], float), p @ np.arange(k), side="right"))
        if "boundary_thresholds" in info:
            reached = [j for j in range(1, k) if p[j:].sum() >= info["boundary_thresholds"][j - 1]]
            grade = max(reached) if reached else 0
        names = (grades.get(GRADE_KEY.get(name, ""), {}) or {}).get("grades") or [str(i) for i in range(k)]
        return {"type": "ordinal", "probabilities": {names[i]: round(float(p[i]), 4) for i in range(k)},
                "expectedGrade": round(float(p @ np.arange(k)), 3), "grade": names[grade], "gradeIndex": grade}
    out = {"type": "regression", "value": round(float(z @ coef + intercept), 3)}
    if name in UNITS:
        out["unit"] = UNITS[name]
    return out


class CustomPredictionService(BasePredictionService):
    models: Dict[str, Any] = {}
    is_initialized: bool = False

    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            return
        cfg = config.models[MODEL_KEY]
        model = EchoAlign(cfg.weights_path)
        CustomPredictionService.models[MODEL_KEY] = model
        CustomPredictionService.meta = json.load(open(cfg.heads_meta_path))
        CustomPredictionService.heads = dict(np.load(cfg.heads_path))
        CustomPredictionService.grades = yaml.safe_load(open(cfg.grades_path))
        prompts = yaml.safe_load(open(cfg.prompts_path))
        CustomPredictionService.zero_shot_text = {
            k: (model.encode_text(prompts[k]["positive"]), model.encode_text(prompts[k]["negative"]),
                prompts[k].get("output", "global"))
            for k in ZERO_SHOT if k in prompts
        }
        CustomPredictionService.batch_size = max(1, int(cfg.clip_batch_size))
        CustomPredictionService.default_selection = cfg.instance_selection
        CustomPredictionService.is_initialized = True
        print("EchoAlign loaded (research use only, CC BY-NC 4.0)")

    def stop_model(self):
        CustomPredictionService.models.clear()
        CustomPredictionService.is_initialized = False
        gc.collect()
        if torch.cuda.is_available():
            torch.cuda.empty_cache()

    # ---------- inputs ----------
    @staticmethod
    def _iter_instances(request: PredictRequest) -> Iterator[Tuple[str, str, bytes]]:
        resolve = getattr(request, "payload", lambda value: value)
        images = request.seriesInstanceImages or {}
        for series in list(images):
            for instance in list(images[series]):
                value = images[series][instance]
                if isinstance(value, dict):
                    value = value.get("image", value)
                data = decode_base64(resolve(value)) if isinstance(value, str) else base64.b64decode(value)
                images[series][instance] = None  # let the payload go while we work
                if data is None:
                    continue
                yield str(series), str(instance), data
        release = getattr(request, "release_payloads", None)
        if release is not None:
            release()

    def _study_outputs(self, g: np.ndarray, c: np.ndarray) -> Tuple[Dict[str, Any], Dict[str, float]]:
        model = self.models[MODEL_KEY]
        study = pool_studies(model.pooler, g, c, group_index=[0] * len(g))[0]
        x = model.pca_embedding(study, "study")
        heads = {n: apply_head(x, n, self.meta, self.heads, self.grades) for n in self.meta if not n.startswith("_")}
        zs = {}
        for k, (pos, neg, slot) in self.zero_shot_text.items():
            i = 0 if slot in (None, "global") else model.structures.index(slot) + 1
            v = study[i] / max(float(np.linalg.norm(study[i])), 1e-12)
            zs[k] = round(float((pos @ v).mean() - (neg @ v).mean()), 4)
        return heads, zs

    def _run_inference(self, request: PredictRequest) -> Dict[str, Any]:
        started = time.time()
        meta = request.additionalMetadata or {}
        selection = str(meta.get("instanceSelection", self.default_selection)).lower()
        model = self.models[MODEL_KEY]
        skipped = {"invalid": 0, "non_image": 0, "tee": 0, "single_frame": 0}
        G: List[np.ndarray] = []
        C: List[np.ndarray] = []
        clips: List[Dict[str, Any]] = []
        batch, labels = [], []

        def flush():
            if batch:
                g, c = model.encode_clips(torch.stack(batch))
                G.extend(g); C.extend(c); clips.extend(labels)
                batch.clear(); labels.clear()

        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "clip.dcm")
            for series, instance, data in self._iter_instances(request):
                try:
                    ds = pydicom.dcmread(BytesIO(data), stop_before_pixels=True)
                except Exception:
                    skipped["invalid"] += 1
                    continue
                reason = select_reason(ds, selection)
                if reason:
                    skipped[reason] += 1
                    continue
                if len(labels) + len(clips) >= MAX_CLIPS:
                    raise ValueError(
                        f"STUDY_TOO_LARGE: study skipped, more than {MAX_CLIPS} usable clips "
                        f"(limit of this research model)"
                    )
                with open(path, "wb") as handle:
                    handle.write(data)
                del data
                try:
                    batch.append(load_echo(path))
                except Exception as exc:
                    print(f"EchoAlign: undecodable clip skipped ({type(exc).__name__})")
                    skipped["invalid"] += 1
                    continue
                labels.append({"series": series, "instance": instance, "frames": frame_count(ds),
                               "seriesNumber": getattr(ds, "SeriesNumber", None) and int(ds.SeriesNumber),
                               "instanceNumber": getattr(ds, "InstanceNumber", None) and int(ds.InstanceNumber)})
                if len(batch) >= self.batch_size:
                    flush()
            flush()
        if not G:
            raise ValueError("No usable transthoracic echo clips in the request")
        g, c = np.asarray(G, np.float32), np.asarray(C, np.float32)
        heads, zs = self._study_outputs(g, c)
        per_clip = []
        for k, label in enumerate(clips):
            h, _ = self._study_outputs(g[k:k + 1], c[k:k + 1])
            per_clip.append({**label,
                             "aorticStenosisProbability": h["aortic_stenosis_classification"]["probability"],
                             "aorticStenosisSevereProbability": h["aortic_stenosis_grade"]["probabilities"].get("severe"),
                             "avPeakVelocity": h["transaortic_peak_velocity"]["value"],
                             "ejectionFraction": h["ejection_fraction"]["value"]})
        asg = heads["aortic_stenosis_grade"]
        summary = {
            "aorticStenosis": {
                "moderateOrSevereProbability": heads["aortic_stenosis_classification"]["probability"],
                "moderateOrSevereCalled": heads["aortic_stenosis_classification"]["called"],
                "grade": asg["grade"],
                "gradeProbabilities": asg["probabilities"],
                "severeProbability": asg["probabilities"].get("severe"),
                "avPeakVelocity": heads["transaortic_peak_velocity"]["value"],
                "meanGradient": heads["transaortic_mean_gradient"]["value"],
                "peakGradient": heads["transaortic_peak_gradient"]["value"],
            },
            "ejectionFraction": heads["ejection_fraction"]["value"],
            "regurgitation": {k: heads[f"{k}_grade"]["grade"]
                              for k in ("aortic_regurgitation", "mitral_regurgitation", "tricuspid_regurgitation")},
        }
        return {
            "researchUseOnly": True, "banner": RESEARCH_BANNER, "attribution": ATTRIBUTION,
            "license": "CC BY-NC 4.0", "instanceSelection": selection, "clipsUsed": len(clips), "skipped": skipped,
            "summary": summary, "keyHeads": {k: heads[k] for k in KEY_HEADS}, "allHeads": heads,
            "zeroShotScores": zs, "perClip": per_clip, "seconds": round(time.time() - started, 1),
        }

    # ---------- outputs ----------
    @staticmethod
    def diagnosis(result: Dict[str, Any]) -> str:
        a = result["summary"]["aorticStenosis"]
        return (f"[Research use only] AS grade {a['grade']} (P severe {a['severeProbability']:.2f}, "
                f"P moderate or severe {a['moderateOrSevereProbability']:.2f}); AV Vmax estimate "
                f"{a['avPeakVelocity']:.2f} m/s; MG estimate {a['meanGradient']:.0f} mmHg; "
                f"LVEF {result['summary']['ejectionFraction']:.0f}%")

    @staticmethod
    def report_inputs(result: Dict[str, Any]) -> Dict[str, Any]:
        """PanEcho 1.1.2 report inputs: diagnosis per finding (PanEcho's keys for shared findings),
        the AS screening block and the recommendation texts."""
        heads = result.get("allHeads") or result.get("keyHeads") or {}
        diagnosis: Dict[str, str] = {}
        for head_name, key in REPORT_FINDINGS:
            head = heads.get(head_name)
            if not head:
                continue
            if head["type"] == "regression":
                diagnosis[key] = f"{head['value']:.1f} {UNITS.get(head_name, head.get('unit', ''))}".strip()
            elif head["type"] == "classification":
                called, not_called = BINARY_WORDING.get(head_name, ("Present", "Absent"))
                diagnosis[key] = f"{called if head['called'] else not_called} (P {head['probability']:.2f})"
            else:
                grade = str(head["grade"]).replace("_", " ")
                p = head["probabilities"].get(head["grade"])
                diagnosis[key] = f"{grade.capitalize()}" + (f" (P {p:.2f})" if p is not None else "")
        asg = heads.get("aortic_stenosis_grade") or {}
        probs = asg.get("probabilities") or {}
        screening = None
        if probs:
            vmax = heads.get("transaortic_peak_velocity") or {}
            screening = {
                "pSevere": float(probs.get("severe", 0.0)),
                "pAny": max(0.0, 1.0 - float(probs.get("normal", 1.0))),
                "avVmaxEstimate": ({"label": "AI estimate, compressed towards the middle of the range",
                                    "value": float(vmax["value"])} if vmax.get("type") == "regression" else None),
                "note": {"en": AS_SCREENING_NOTE_EN, "fr": AS_SCREENING_NOTE_FR},
            }
        return {
            "banner": RESEARCH_BANNER,
            "diagnosis": diagnosis,
            "asScreening": screening,
            "probability": {},
            "recommendations": {"en": RESEARCH_BANNER + ". " + ATTRIBUTION,
                                "fr": "Recherche seulement \u2014 EchoAlign (CC BY-NC 4.0), pas pour des d\u00e9cisions cliniques. " + ATTRIBUTION},
        }

    @staticmethod
    def render_html(result: Dict[str, Any]) -> str:
        return HTMLParser.generate_detection_results(CustomPredictionService.report_inputs(result))

    async def _handle_json_output(self, request: PredictRequest):
        result = self._run_inference(request)
        return {
            "predictions": result,
            "diagnosis": self.diagnosis(result),
            "modelRecommendations": {"en": RESEARCH_BANNER,
                                     "fr": "Recherche seulement — EchoAlign (CC BY-NC 4.0), pas pour des décisions cliniques",
                                     "presentable": False},
        }

    async def _handle_html_output(self, request: PredictRequest):
        result = self._run_inference(request)
        return {"htmlBase64": base64.b64encode(self.render_html(result).encode("utf-8")).decode("ascii")}
