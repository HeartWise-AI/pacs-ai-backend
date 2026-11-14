"""DeepCORO-JEPA: one EchoJEPA video encoder that segments the coronary tree itself and reads 18-segment
stenosis (+ LAD FFR/iFR, + lesion types when the checkpoint has those heads) from a diagnostic angiography study.

Not a CLIP/MIL model: there is no zero-video padding. The model takes the real videos as a set and runs the encoder
at most twice per study (full views at 224 px, then tracked 320 px crops per segment); `measured_forward` enforces
the two-pass budget. Yes/no calls use the thresholds.json shipped with the pinned model revision (chosen on the
validation set): a finding is positive when its probability is strictly greater than its threshold. Stenosis has one
threshold per segment; calcification, thrombus and chronic total occlusion have one threshold per finding for all
segments. Research use only.
"""
import base64
import json
import os
import sys
import tempfile
from io import BytesIO
from pathlib import Path
from types import SimpleNamespace
from typing import Optional

import cv2 as cv
import numpy as np
import pydicom
import torch
import display_calibration
from explain import capture_pooling, explain_segments
from report_html import HIDDEN_SEGMENTS, LESION_NAMES, PROBABILITY_DECIMALS, SEGMENT_NAMES, render_report, shown  # noqa: F401
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

MODELS_DIR = Path(__file__).resolve().parent / "models"
sys.path.insert(0, str(MODELS_DIR / "deepcoro_model"))

SERIES_TIME_TAG = (0x0008, 0x0031)
FPS_TAG = (0x0008, 0x2144)

PRESSURE_OPERATING_POINTS = ("balanced", "high_sensitivity", "high_specificity")
LESION_OPERATING_POINTS = ("balanced", "specificity_95", "specificity_99")
DOMINANCE_METHODS = ("majority", "tie_mean_probability")
MAX_EXPLAINED_SEGMENTS = 6
NO_VIDEO_HTML = "<h1>No video could be extracted from the current DICOM series</h1>"


def _count(value) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value >= 0


class CustomPredictionService(BasePredictionService):
    model_config: dict = {}
    structure: dict = {}
    thresholds: dict = {}

    # -- lifecycle ---------------------------------------------------------

    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            return
        from safetensors.torch import load_file
        from integrated_multilayer import multilayer_model_from_payload

        with open(MODELS_DIR / "config.json") as fp:
            CustomPredictionService.model_config = json.load(fp)
        weights = MODELS_DIR / CustomPredictionService.model_config["ModelStateDict"]["model_path"]
        structure = json.loads((weights.parent / "model_config.json").read_text())
        payload = {"bundle_schema": structure["bundle_schema"], "groups": tuple(structure["groups"]),
                   "decoder_width": structure["decoder_width"],
                   "block_indices_zero_based": structure["block_indices_zero_based"],
                   "physiology": structure.get("physiology"), "state_dict": load_file(str(weights))}
        CustomPredictionService.thresholds = self.load_thresholds(
            weights.parent, CustomPredictionService.model_config["Thresholds"], structure["groups"],
            physiology=bool(structure.get("physiology")), lesion=bool(structure.get("heads", {}).get("lesion")),
            weights_sha256=CustomPredictionService.get_model_info()["provenance"]["weightsSha256"])
        device = "cuda" if torch.cuda.is_available() else "cpu"
        model = multilayer_model_from_payload(payload, device=device, train_blocks=0)
        model.eval()
        CustomPredictionService.models["deepcoro_jepa"] = model
        CustomPredictionService.structure = structure
        CustomPredictionService.is_initialized = True
        print(f"DeepCORO-JEPA {structure.get('model_version')} loaded on {device}; heads {structure.get('heads')}")

    @staticmethod
    def load_thresholds(models_dir, settings: dict, groups, *, physiology: bool, lesion: bool, weights_sha256: str) -> dict:
        """Decision thresholds of the pinned model revision, one per output. `settings` is the "Thresholds" block of
        models/config.json. Fails on an unknown key or operating point, when the file belongs to other weights or when
        an output has no threshold: no silent default. `lesion_operating_point` is checked for every checkpoint and
        used by those with a lesion head."""
        keys = {"path", "pressure_operating_point", "lesion_operating_point"}
        if set(settings) != keys:
            raise ValueError(f'"Thresholds" takes exactly {sorted(keys)}, got {sorted(settings)}')
        pressure_operating_point = settings["pressure_operating_point"]
        if pressure_operating_point not in PRESSURE_OPERATING_POINTS:
            raise ValueError(f"Unknown pressure_operating_point {pressure_operating_point!r}; choose one of {PRESSURE_OPERATING_POINTS}")
        lesion_operating_point = settings["lesion_operating_point"]
        if lesion_operating_point not in LESION_OPERATING_POINTS:
            raise ValueError(f"Unknown lesion_operating_point {lesion_operating_point!r}; choose one of {LESION_OPERATING_POINTS}")
        path = Path(models_dir) / settings["path"]
        spec = json.loads(path.read_text(encoding="utf-8"))
        if spec["weights_sha256"] != weights_sha256:
            raise ValueError(f"{path} was derived for weights {spec['weights_sha256']}, not {weights_sha256}")
        wanted = {f"{g}_prob_significant": "balanced" for g in groups}
        if physiology:
            wanted.update({"ffr_abnormal_prob": pressure_operating_point, "ifr_abnormal_prob": pressure_operating_point})
        if lesion:
            wanted.update({f"{name}_prob": lesion_operating_point for name in LESION_NAMES})
        missing = [name for name, point in wanted.items() if point not in spec["thresholds"].get(name, {})]
        if missing:
            raise ValueError(f"{path} has no threshold for {missing}")
        out = {name: float(spec["thresholds"][name][point]) for name, point in wanted.items()}
        if any(not 0.0 < t < 1.0 for t in out.values()):
            raise ValueError(f"{path} has a threshold outside (0, 1)")
        return {"pressure_operating_point": pressure_operating_point, "lesion_operating_point": lesion_operating_point,
                "values": out}

    def stop_model(self):
        CustomPredictionService.models.clear()
        CustomPredictionService.is_initialized = False
        if torch.cuda.is_available():
            torch.cuda.empty_cache()

    # -- video selection and conversion -----------------------------------

    def _decode_dicoms(self, request: PredictRequest) -> list[pydicom.Dataset]:
        dicoms: list[pydicom.Dataset] = []
        for series_number in request.seriesInstanceImages or {}:
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

    @staticmethod
    def _series_time(dicom: pydicom.Dataset) -> float:
        try:
            return float(str(dicom[SERIES_TIME_TAG].value))
        except Exception:
            return float("inf")

    @staticmethod
    def _training_order_key(dicom: pydicom.Dataset) -> str:
        # Training videos were '<StudyInstanceUID>_<SOPInstanceUID>.dcm.avi', read in filename order. Crop selection
        # and tie-breaks depend on order, so the selected videos are fed in that same order.
        return f"{getattr(dicom, 'StudyInstanceUID', '')}_{getattr(dicom, 'SOPInstanceUID', '')}"

    def select_videos(self, dicoms: list[pydicom.Dataset]) -> list[pydicom.Dataset]:
        """SeriesTime order picks which runs are used (the backend orders by UID suffix, not time), capped at
        dicomUploadMax; the chosen runs are then ordered as in training."""
        cap = int(CustomPredictionService.get_model_info().get("dicomUploadMax", 12))
        chosen = sorted(dicoms, key=self._series_time)[:cap]
        return sorted(chosen, key=self._training_order_key)

    @staticmethod
    def dicom_to_avi(dicom: pydicom.Dataset, output_path: str) -> Optional[str]:
        """Exactly the training conversion: 8-bit grayscale pixel array -> MJPG AVI at the DICOM frame rate."""
        try:
            video = dicom.pixel_array
            photometric = dicom.PhotometricInterpretation
            if photometric == "RGB" and video.ndim == 4:  # training wrote RGB as BGR and read it back as gray
                video = np.stack([cv.cvtColor(f, cv.COLOR_RGB2GRAY) for f in video])
            elif photometric not in ("MONOCHROME1", "MONOCHROME2", "PALETTE COLOR"):
                return None  # PALETTE COLOR: training stored the raw palette indices as gray levels
            if video.ndim != 3:
                return None
            if video.dtype != np.uint8:
                video = cv.normalize(video, None, 0, 255, cv.NORM_MINMAX).astype(np.uint8)
            fps = float(dicom[FPS_TAG].value) if FPS_TAG in dicom else 30.0
            out = cv.VideoWriter(output_path, cv.VideoWriter_fourcc(*"MJPG"), fps, (video.shape[2], video.shape[1]))
            for frame in video:
                out.write(cv.cvtColor(frame, cv.COLOR_GRAY2BGR))
            out.release()
            return output_path
        except Exception as e:
            print(f"dicom_to_avi error: {e}")
            return None

    # -- inference ----------------------------------------------------------

    def _run_inference(self, dicoms: list[pydicom.Dataset]) -> Optional[dict]:
        from integrated_data import load_video_clip
        from integrated_study import measured_forward
        model = CustomPredictionService.models["deepcoro_jepa"]
        cfg = CustomPredictionService.model_config["Forward"]
        args = SimpleNamespace(device=next(model.parameters()).device.type, full_res=cfg["full_res"], crop_res=cfg["crop_res"],
                               max_views=cfg["max_views"], crops_per_group=cfg["crops_per_group"],
                               frame_sampling=cfg["frame_sampling"], train_blocks=0)
        clips, clip_dicoms = [], []
        with tempfile.TemporaryDirectory() as tmp:
            for i, dicom in enumerate(self.select_videos(dicoms)):
                path = self.dicom_to_avi(dicom, os.path.join(tmp, f"{i:02d}.avi"))
                if path is None:
                    continue
                try:
                    clips.append(load_video_clip(path, sampling=cfg["frame_sampling"]))
                    clip_dicoms.append(dicom)
                except Exception as e:
                    print(f"load_video_clip failed: {e}")
        if not clips:
            return None
        store = {}
        try:
            with torch.inference_mode(), capture_pooling(model) as store:
                res = measured_forward(model, clips, args)
        except Exception:
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise  # predict() answers 500 PROCESSING_ERROR; None is kept for "no usable clip"
        preds = self.postprocess(model.groups, res, len(clips), CustomPredictionService.thresholds)
        preds["explainability"] = self.explanations(model, store, clips, clip_dicoms, preds, cfg["crop_res"])
        return preds

    @staticmethod
    def explanations(model, store: dict, clips: list, clip_dicoms: list, preds: dict, crop_res: int) -> list[dict]:
        """Within-crop attention for the flagged stenosis segments (significant or CTO), most probable first.
        Never fails the prediction: an error is logged and the report goes out without the crops."""
        segs = preds["segments"]
        targets = sorted((g for g, v in segs.items() if g not in HIDDEN_SEGMENTS and (v["significant"] or v.get("cto"))),
                         key=lambda g: -segs[g]["prob_significant"])[:MAX_EXPLAINED_SEGMENTS]
        try:
            with torch.inference_mode():
                items = explain_segments(model, store, clips, targets, crop_res)
        except Exception as e:
            print(f"explainability failed: {e!r}")
            return []
        for item in items:
            if item.get("has_crop"):
                dicom = clip_dicoms[item["video"]]
                item["series_instance_uid"] = str(getattr(dicom, "SeriesInstanceUID", ""))
                item["sop_instance_uid"] = str(getattr(dicom, "SOPInstanceUID", ""))
        return items

    @staticmethod
    def public(preds: Optional[dict]) -> Optional[dict]:
        """The JSON response. Explanation items keep their JPEG `images` (base64 strings) so that
        /inference/render-report can draw the same report from a stored result; any private `_images` key is dropped."""
        if preds is None or "explainability" not in preds:
            return preds
        return {**preds, "explainability": [{k: v for k, v in item.items() if k != "_images"} for item in preds["explainability"]]}

    @staticmethod
    def postprocess(groups, res: dict, n_videos: int, thresholds: dict) -> dict:
        """`thresholds` comes from load_thresholds. A call is positive when the returned probability (rounded to
        PROBABILITY_DECIMALS) is strictly greater than its threshold, so every flag can be recomputed from the
        response."""
        cut = thresholds["values"]
        out = {"n_videos": n_videos, "encoder_passes": int(res["encoder_passes"]), "segments": {}}
        sev = res["severity"].float().cpu().tolist()
        prob = [round(x, PROBABILITY_DECIMALS) for x in res["binary_probability"].float().cpu().tolist()]
        for i, g in enumerate(groups):
            out["segments"][g] = {"stenosis_pct": round(min(max(sev[i], 0.0), 100.0), 1),
                                  "prob_significant": prob[i],
                                  "prob_threshold": cut[f"{g}_prob_significant"],
                                  "significant": bool(prob[i] > cut[f"{g}_prob_significant"]),
                                  "significant_threshold_pct": 50 if g == "left_main" else 70}
        if "physiology" in res:
            ph = {k: float(v.float().cpu().reshape(-1)[0]) for k, v in res["physiology"].items()}
            prob_abnormal = {k: round(float(1 / (1 + np.exp(-ph[f"{k}_logit"]))), PROBABILITY_DECIMALS)
                             for k in ("ffr", "ifr")}
            out["lad_physiology"] = {"ffr": round(ph["ffr"], 3), "ifr": round(ph["ifr"], 3),
                                     "operating_point": thresholds["pressure_operating_point"]}
            for k in ("ffr", "ifr"):
                out["lad_physiology"].update({f"{k}_abnormal_prob": prob_abnormal[k],
                                              f"{k}_abnormal_threshold": cut[f"{k}_abnormal_prob"],
                                              f"{k}_abnormal": bool(prob_abnormal[k] > cut[f"{k}_abnormal_prob"])})
        for name in LESION_NAMES:
            if f"lesion_{name}_probability" in res:
                out.setdefault("lesion_thresholds", {"operating_point": thresholds["lesion_operating_point"]})[name] = cut[f"{name}_prob"]
                for g, x in zip(groups, res[f"lesion_{name}_probability"].float().cpu().tolist()):
                    x = round(float(x), PROBABILITY_DECIMALS)
                    out["segments"][g].update({f"{name}_prob": x, name: bool(x > cut[f"{name}_prob"])})
        display_calibration.add_display(out["segments"])  # display only: stenosis_pct and the flags are unchanged
        return out

    # -- reporting -------------------------------------------------------------

    @staticmethod
    def significant_segments(preds: dict) -> list[str]:
        return [g for g, v in preds["segments"].items() if v["significant"]]

    def diagnosis_text(self, preds: dict) -> str:
        # The report leaves out HIDDEN_SEGMENTS (posterior LV branch); a CTO-flagged segment is reported as 100%.
        segs = {g: v for g, v in preds["segments"].items() if g not in HIDDEN_SEGMENTS}
        sig = [g for g in self.significant_segments(preds) if g in segs]
        cal = display_calibration
        pct = lambda v: (f"shown 100%, CTO flag; model estimate {v['stenosis_pct']:.0f}%" if v.get("cto")
                         else f"shown {cal.display_pct(v)}%, model estimate {v['stenosis_pct']:.0f}%")
        text = ("Flagged for possible significant stenosis (probability above its validation threshold): "
                + ", ".join(f"{SEGMENT_NAMES[g]} (P {shown(segs[g]['prob_significant'], segs[g]['prob_threshold'], 3)}, "
                            f"{pct(segs[g])})" for g in sig) + "."
                if sig else "No segment flagged for significant stenosis.")
        others = [g for g in segs if g not in sig]
        marked = [g for g in others if cal.display_pct(segs[g]) >= 30]
        if marked:
            text += (" Other segments with a shown stenosis of 30% or more: "
                     + ", ".join(f"{SEGMENT_NAMES[g]} ({cal.display_pct(segs[g])}%)" for g in marked) + ".")
        normal = sum(cal.display_pct(v) == 0 for v in segs.values())
        text += (f" {normal} of {len(segs)} segments shown as normal (model estimate below "
                 f"{cal.calibration()['zero_below_pct']:g}%).")
        if "lad_physiology" in preds:
            p = preds["lad_physiology"]
            call = lambda k: "abnormal" if p[f"{k}_abnormal"] else "normal"
            prob = lambda k: shown(p[f"{k}_abnormal_prob"], p[f"{k}_abnormal_threshold"], 2)
            text += (f" LAD physiology: FFR predicted {call('ffr')} (P(FFR ≤ 0.80) {prob('ffr')}, threshold "
                     f"{p['ffr_abnormal_threshold']:.4f}); iFR predicted {call('ifr')} (P(iFR ≤ 0.89) "
                     f"{prob('ifr')}, threshold {p['ifr_abnormal_threshold']:.4f}).")
        for name, label in LESION_NAMES.items() if "lesion_thresholds" in preds else ():
            hit = [g for g, v in segs.items() if v[name]]
            cut = preds["lesion_thresholds"][name]
            text += (f" Flagged for {label} (P above {cut:.4f}): "
                     + ", ".join(f"{SEGMENT_NAMES[g]} (P {shown(segs[g][f'{name}_prob'], cut, 2)})" for g in hit) + "."
                     if hit else f" No segment flagged for {label}.")
        if dom := preds.get("coronary_dominance"):
            text += (f" Coronary dominance: {dom['call']} (angiogram classifier, "
                     + (f"majority of {dom['video_count']} diagnostic videos: {dom['left_votes']} left, {dom['right_votes']} right)."
                        if dom["method"] == "majority" else
                        f"tied {dom['left_votes']}/{dom['right_votes']}, mean P(left) {dom['mean_left_probability']:.2f}).")
                     )
        return text

    @staticmethod
    def recommendations(preds: dict) -> dict:
        en = ("Research model, not for clinical decisions. Flags use thresholds chosen on the internal validation set, "
              "not a validated clinical operating point. Confirm every reading on the angiogram; LAD physiology "
              "outputs (test AUROC about 0.69) do not replace invasive FFR/iFR. Calcification, thrombus and chronic "
              "total occlusion flags are set for few false alarms and miss many of these findings.")
        fr = ("Modèle de recherche, ne pas utiliser pour la décision clinique. Les signalements utilisent des seuils "
              "choisis sur l'ensemble de validation interne, et non un seuil clinique validé. Confirmer chaque lecture sur "
              "l'angiographie; les sorties de physiologie IVA (AUROC test environ 0,69) ne remplacent pas la FFR/iFR invasive. "
              "Les signalements de calcification, de thrombus et d'occlusion totale chronique sont réglés pour peu de "
              "fausses alertes et manquent une bonne partie de ces lésions.")
        return {"en": en, "fr": fr, "presentable": True}

    @staticmethod
    def report_model_version() -> str:
        """Version printed in the report header: the model version and the display calibration ("v13-cal1")."""
        return f"{CustomPredictionService.model_version()}-{display_calibration.version()}"

    @staticmethod
    def model_version() -> str:
        """Model version ("v13"): from the loaded checkpoint structure, else from the model_config.json downloaded
        next to the weights (no load needed), else from the model facts."""
        version = CustomPredictionService.structure.get("model_version")
        if version:
            return str(version)
        try:
            config = json.loads((MODELS_DIR / "config.json").read_text(encoding="utf-8"))
            structure = MODELS_DIR / config["ModelStateDict"].get("structure_path", "model_config.json")
            version = json.loads(structure.read_text(encoding="utf-8")).get("model_version")
            if version:
                return str(version)
        except (OSError, ValueError, KeyError):
            pass
        facts = json.loads((MODELS_DIR.parent / "data" / "model_facts.json").read_text(encoding="utf-8"))
        return str(facts["en"]["Summary"].get("Model_version", ""))

    @classmethod
    def report_html(cls, preds: Optional[dict]) -> str:
        """The HTML report of a JSON-mode `predictions` object. HTML-mode predict and /inference/render-report both
        call this on the public (JSON) predictions, so a stored JSON result renders to the same document."""
        if not preds:
            return NO_VIDEO_HTML
        return render_report(preds, cls.report_model_version(), cls.recommendations(preds)["en"])

    def render_html(self, preds: dict) -> str:
        return self.report_html(self.public(preds))

    @staticmethod
    def coronary_dominance(request: PredictRequest) -> Optional[dict]:
        """Study dominance passed by the caller in additionalMetadata.coronaryDominance: the majority call of the
        angiogram procedure classifier over the diagnostic videos sent here. Absent -> None; malformed -> error."""
        dom = (getattr(request, "additionalMetadata", None) or {}).get("coronaryDominance")
        if dom is None:
            return None
        if not isinstance(dom, dict) or dom.get("call") not in ("left", "right") or dom.get("method") not in DOMINANCE_METHODS:
            raise ValueError("coronaryDominance needs call left/right and a known method")
        left, right, n = dom.get("leftVotes"), dom.get("rightVotes"), dom.get("videoCount")
        mean = dom.get("meanLeftProbability")
        if not (_count(left) and _count(right) and _count(n)) or n == 0 or left + right != n:
            raise ValueError("coronaryDominance vote counts are inconsistent")
        if mean is not None and (isinstance(mean, bool) or not isinstance(mean, (int, float)) or not 0 <= mean <= 1):
            raise ValueError("coronaryDominance meanLeftProbability must be within [0, 1]")
        majority = "left" if left > right else "right" if right > left else None
        if (dom["method"] == "majority") != (majority is not None) or (majority and dom["call"] != majority) or (
                majority is None and (mean is None or dom["call"] != ("left" if mean >= 0.5 else "right"))):
            raise ValueError("coronaryDominance call does not follow its votes")
        return {"call": dom["call"], "method": dom["method"], "left_votes": left, "right_votes": right,
                "video_count": n, "mean_left_probability": mean, "source": str(dom.get("source", ""))[:200]}

    def _predict(self, request: PredictRequest) -> Optional[dict]:
        dominance = self.coronary_dominance(request)  # validated before the GPU runs
        preds = self._run_inference(self._decode_dicoms(request))
        if preds is not None and dominance is not None:
            preds["coronary_dominance"] = dominance
        return preds

    async def _handle_json_output(self, request: PredictRequest):
        preds = self._predict(request)
        if preds is None:
            return {"diagnosis": "No video could be extracted from the current DICOM series", "predictions": {},
                    "modelRecommendations": {"en": "No video could be extracted or processed.",
                                             "fr": "Aucune vidéo n'a pu être extraite ou traitée.", "presentable": False}}
        return {"diagnosis": self.diagnosis_text(preds), "predictions": self.public(preds),
                "modelRecommendations": self.recommendations(preds)}

    async def _handle_html_output(self, request: PredictRequest):
        preds = self._predict(request)
        html = self.report_html(self.public(preds))
        return {"htmlBase64": base64.b64encode(html.encode("utf-8")).decode("utf-8")}
