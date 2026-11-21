"""DeepCORO-JEPA: one EchoJEPA video encoder that segments the coronary tree itself and reads 18-segment
stenosis (+ LAD FFR/iFR, + lesion types, + study-level lesion probabilities and coronary dominance when the
checkpoint has those heads) from a diagnostic angiography study.

Not a CLIP/MIL model: there is no zero-video padding. The model takes the real videos as a set and runs the encoder
at most twice per study (full views at 224 px, then tracked 320 px crops per segment); `measured_forward` enforces
the two-pass budget. Yes/no calls use the thresholds.json shipped with the pinned model revision (chosen on the
validation set): a finding is positive when its probability is strictly greater than its threshold. Stenosis has one
threshold per segment; calcification, thrombus and chronic total occlusion have one threshold per finding for all
segments. Outputs that model_config.json lists under `unsupported_outputs` (v15: the posterior LV branch, which had
no training target) keep their keys in the JSON but never raise a flag and are never displayed.

From v19 (container 2.2.0): a per-segment P(occluded) shown as "100% (occluded)" above its display threshold on the
epicardial segments, a "possible stent" annotation from the procedure head (annotation only), the LAD FFR/iFR
measurement region (proximal and mid LAD), and an optional side check: when the caller sends the view label of each
run (`additionalMetadata.coronaryViews`), the segments of a coronary side with no run among those read are returned
as not assessed. Research use only.
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
OPERATING_POINT_MAX_CHARS = 64  # limit of report_input for lesion_thresholds.operating_point
DOMINANCE_METHODS = ("majority", "tie_mean_probability")
DOMINANCE_OUTPUT = "dominance_left_prob"  # thresholds.json name of the model's own dominance output (v15 on)
DOMINANCE_LABEL_SOURCE = "model-derived labels (ObjectRecon RCA-video majority), not expert reads"
# LAD FFR/iFR test AUROC quoted in the recommendation, by model version (a stored result keeps its own model's figure)
PRESSURE_TEST_AUROC = {"v13": ("0.69", "0,69"), "v15": ("0.67", "0,67"), "v19": ("0.71", "0,71")}
OCCLUSION_TAGS = ("CTO", "acute/other")
# LAD FFR/iFR labels: measurements in the proximal or mid LAD only (pressure head trained on those, PI 2026-11-13)
PRESSURE_REGION = ("prox_lad", "mid_lad")
PRESSURE_REGION_LABEL = "proximal and mid LAD"
RIGHT_SEGMENTS = ("prox_rca", "mid_rca", "dist_rca")
LEFT_SEGMENTS = ("left_main", "prox_lad", "mid_lad", "dist_lad", "D1", "D2", "prox_lcx", "mid_lcx", "dist_lcx", "om1", "om2", "bx")
DOMINANCE_SEGMENTS = ("pda", "posterolateral", "lvp")  # on the side of the dominant artery
SIDE_VIEW_LABELS = {"Left Coronary": "left", "Right Coronary": "right"}
NOT_ASSESSED_REASON = {"left": "no left coronary run", "right": "no right coronary run"}
MAX_VIEW_ENTRIES = 64
MAX_EXPLAINED_SEGMENTS = 6
NO_VIDEO_HTML = "<h1>No video could be extracted from the current DICOM series</h1>"


def not_assessed(preds: dict) -> set:
    """Segments the side check marked as not assessed (no run of their coronary side among the runs read)."""
    return {g for g, v in (preds.get("segments") or {}).items() if v.get("assessed") is False}


def pressure_region_sentence(p: dict, segs: dict) -> str:
    """For a positive FFR/iFR call: the measurement region and the stenosis shown there."""
    def seg(g):
        v = segs.get(g)
        if v is None:
            return f"{SEGMENT_NAMES[g].lower()} not shown"
        return (f"{SEGMENT_NAMES[g].lower()} shown {v['display_pct']}%"
                + (" (occluded)" if v["display_category"] == "occluded" else "")
                + f" ({'flagged' if v['significant'] else 'not flagged'})")
    return ("Predicted abnormal FFR/iFR refers to the proximal-mid LAD (the measurement region in training, not a "
            f"localisation of the culprit lesion by the model): {seg('prox_lad')}, {seg('mid_lad')}.")


def _count(value) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value >= 0


class CustomPredictionService(BasePredictionService):
    model_config: dict = {}
    structure: dict = {}
    thresholds: dict = {}
    unsupported: tuple = ()

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
        heads = structure.get("heads", {})
        thresholds = self.load_thresholds(
            weights.parent, CustomPredictionService.model_config["Thresholds"], structure["groups"],
            physiology=bool(structure.get("physiology")), lesion=bool(heads.get("lesion")),
            dominance=bool(heads.get("dominance")), occluded=bool(heads.get("occluded")),
            procedure=bool(heads.get("procedure")),
            weights_sha256=CustomPredictionService.get_model_info()["provenance"]["weightsSha256"])
        unsupported = self.unsupported_segments(structure)
        display_calibration.calibration(str(structure["model_version"]))  # no mapping for this model: stop here
        device = "cuda" if torch.cuda.is_available() else "cpu"
        model = multilayer_model_from_payload(payload, device=device, train_blocks=0)
        model.eval()
        for head, attribute in (("lesion", "lesion_head"), ("lesion_pooler", "lesion_pooler"),
                                ("dominance", "dominance_head"), ("procedure", "procedure_head"),
                                ("occluded", "occluded_head")):
            if head in heads and bool(heads[head]) != (getattr(model, attribute, None) is not None):
                raise ValueError(f"model_config.json says heads.{head} = {heads[head]} but the weights say otherwise")
        CustomPredictionService.models["deepcoro_jepa"] = model
        CustomPredictionService.thresholds = thresholds
        CustomPredictionService.unsupported = unsupported
        CustomPredictionService.structure = structure
        CustomPredictionService.is_initialized = True
        print(f"DeepCORO-JEPA {structure.get('model_version')} loaded on {device}; heads {structure.get('heads')}")

    @staticmethod
    def unsupported_segments(structure: dict) -> tuple:
        """Segments that model_config.json lists under `unsupported_outputs` (v15: lvp, no training target)."""
        names = tuple(structure.get("unsupported_outputs") or ())
        unknown = [g for g in names if g not in structure["groups"]]
        if unknown:
            raise ValueError(f"model_config.json lists unsupported outputs that are not segments: {unknown}")
        return names

    @staticmethod
    def load_thresholds(models_dir, settings: dict, groups, *, physiology: bool, lesion: bool, weights_sha256: str,
                        dominance: bool = False, occluded: bool = False, procedure: bool = False) -> dict:
        """Decision thresholds of the pinned model revision, one per output. `settings` is the "Thresholds" block of
        models/config.json. Fails on an unknown key or operating point, when the file belongs to other weights or when
        an output has no threshold: no silent default. `lesion_operating_point` is checked for every checkpoint and
        used by those with a lesion head. The optional `lesion_threshold_overrides` ({"thrombus": 0.4}) replaces the
        threshold of the named lesion findings by a fixed value; the operating point returned with a prediction then
        names them ("specificity_99 (thrombus: fixed 0.40)"). A checkpoint with a dominance head (v15 on) also needs
        the `balanced` threshold of `dominance_left_prob`. A checkpoint with an occluded head (v19 on) needs
        `occluded_operating_point` (a key of thresholds.json `occluded_prob`) and `occluded_segments` (the segments
        that may be shown 100% occluded); one with a procedure head may set `stent_annotation_threshold`."""
        keys = {"path", "pressure_operating_point", "lesion_operating_point"}
        optional = {"lesion_threshold_overrides", "occluded_operating_point", "occluded_segments", "stent_annotation_threshold"}
        if not keys <= set(settings) <= keys | optional:
            raise ValueError(f'"Thresholds" takes {sorted(keys)} and optionally {sorted(optional)}, got {sorted(settings)}')
        overrides = settings.get("lesion_threshold_overrides", {})
        if not isinstance(overrides, dict) or set(overrides) - set(LESION_NAMES):
            raise ValueError(f'"lesion_threshold_overrides" takes thresholds for {list(LESION_NAMES)}, got {overrides!r}')
        if any(isinstance(t, bool) or not isinstance(t, (int, float)) or not 0.0 < t < 1.0 for t in overrides.values()):
            raise ValueError('"lesion_threshold_overrides" needs thresholds strictly between 0 and 1')
        if any(round(t, 2) != t for t in overrides.values()):
            raise ValueError('"lesion_threshold_overrides" takes thresholds with at most 2 decimals (they are named in the operating point)')
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
        if dominance:
            wanted[DOMINANCE_OUTPUT] = "balanced"
        missing = [name for name, point in wanted.items() if point not in spec["thresholds"].get(name, {})]
        if missing:
            raise ValueError(f"{path} has no threshold for {missing}")
        out = {name: float(spec["thresholds"][name][point]) for name, point in wanted.items()}
        if any(not 0.0 < t < 1.0 for t in out.values()):
            raise ValueError(f"{path} has a threshold outside (0, 1)")
        if lesion and overrides:
            out.update({f"{name}_prob": float(t) for name, t in overrides.items()})
            fixed = ", ".join(f"{name}: fixed {overrides[name]:.2f}" for name in LESION_NAMES if name in overrides)
            lesion_operating_point = f"{lesion_operating_point} ({fixed})"
            if len(lesion_operating_point) > OPERATING_POINT_MAX_CHARS:  # report_input rejects longer stored results
                raise ValueError(f'"lesion_threshold_overrides" names too many findings: {lesion_operating_point!r} '
                                 f"exceeds {OPERATING_POINT_MAX_CHARS} characters")
        result = {"pressure_operating_point": pressure_operating_point, "lesion_operating_point": lesion_operating_point,
                  "values": out}
        if occluded:
            point, segments = settings.get("occluded_operating_point"), settings.get("occluded_segments")
            if not isinstance(point, str) or point not in spec["thresholds"].get("occluded_prob", {}):
                raise ValueError(f"{path} has no occluded_prob threshold {point!r} (Thresholds.occluded_operating_point)")
            if (not isinstance(segments, list) or not segments or len(set(segments)) != len(segments)
                    or set(segments) - set(groups)):
                raise ValueError('"occluded_segments" must list distinct segments of the model')
            cut = float(spec["thresholds"]["occluded_prob"][point])
            if not 0.0 < cut < 1.0:
                raise ValueError(f"{path} has an occluded_prob threshold outside (0, 1)")
            result["occluded"] = {"threshold": cut, "operating_point": point,
                                  "segments": [g for g in groups if g in segments]}
        stent = settings.get("stent_annotation_threshold")
        if stent is not None:
            if not procedure:
                raise ValueError('"stent_annotation_threshold" needs a checkpoint with a procedure head')
            if isinstance(stent, bool) or not isinstance(stent, (int, float)) or not 0.0 < stent < 1.0:
                raise ValueError('"stent_annotation_threshold" must be strictly between 0 and 1')
            result["stent_annotation"] = float(stent)
        return result

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
        preds = self.postprocess(model.groups, res, len(clips), CustomPredictionService.thresholds,
                                 model_version=CustomPredictionService.model_version(),
                                 unsupported=CustomPredictionService.unsupported)
        preds["explainability"] = self.explanations(model, store, clips, clip_dicoms, preds, cfg["crop_res"])
        preds["_used_sops"] = [str(getattr(d, "SOPInstanceUID", "")) for d in clip_dicoms]  # dropped by _predict
        return preds

    @staticmethod
    def explanations(model, store: dict, clips: list, clip_dicoms: list, preds: dict, crop_res: int) -> list[dict]:
        """Within-crop attention for the flagged stenosis segments (significant or CTO), most probable first.
        Never fails the prediction: an error is logged and the report goes out without the crops."""
        segs = preds["segments"]
        targets = sorted((g for g, v in segs.items() if g not in HIDDEN_SEGMENTS
                          and (v["significant"] or v.get("cto") or v.get("occluded"))),
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
    def postprocess(groups, res: dict, n_videos: int, thresholds: dict, model_version: Optional[str] = None,
                    unsupported=()) -> dict:
        """`thresholds` comes from load_thresholds. A call is positive when the returned probability (rounded to
        PROBABILITY_DECIMALS) is strictly greater than its threshold, so every flag can be recomputed from the
        response. The exception is a segment in `unsupported` (model_config.json `unsupported_outputs`): the model
        has no training target for it, so its numbers are returned as the model wrote them but its four flags are
        always false, it gets `unsupported: true` and no display fields.

        `model_version` (default: the served one) is returned as `predictions.model_version`; it selects the display
        mapping now and when the stored result is drawn again by a later container."""
        model_version = str(model_version or CustomPredictionService.model_version())
        cut = thresholds["values"]
        out = {"model_version": model_version, "n_videos": n_videos, "encoder_passes": int(res["encoder_passes"]),
               "segments": {}}
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
        # Study-level lesion probability (any segment), from the study lesion pooler: a probability, no yes/no call.
        study = {f"study_{name}_prob": round(float(res[f"lesion_{name}_study_logit"].float().sigmoid().cpu().reshape(-1)[0]),
                                             PROBABILITY_DECIMALS)
                 for name in LESION_NAMES if f"lesion_{name}_study_logit" in res}
        if study:
            out["study_lesions"] = study
        # The model's own coronary dominance estimate. It is separate from `coronary_dominance` (the caller-supplied
        # majority of the angiogram classifier), which alone decides how the schema is drawn.
        if "dominance_probability" in res:
            p = round(float(res["dominance_probability"].float().cpu().reshape(-1)[0]), PROBABILITY_DECIMALS)
            out["model_dominance"] = {"dominance_left_prob": p, "dominance_left_threshold": cut[DOMINANCE_OUTPUT],
                                      "dominance_left_pred": bool(p > cut[DOMINANCE_OUTPUT]),
                                      "operating_point": "balanced", "label_source": DOMINANCE_LABEL_SOURCE}
        # v19: P(occluded) per segment; "100% (occluded)" on the listed (epicardial) segments above the display
        # threshold, tagged CTO when the CTO flag is set, else acute/other.
        if "occluded_probability" in res and "occluded" in thresholds:
            occ = thresholds["occluded"]
            out["occluded_threshold"] = {"threshold": occ["threshold"], "operating_point": occ["operating_point"],
                                         "segments": list(occ["segments"])}
            for g, x in zip(groups, res["occluded_probability"].float().cpu().tolist()):
                v = out["segments"][g]
                x = round(float(x), PROBABILITY_DECIMALS)
                hit = g in occ["segments"] and x > occ["threshold"]
                v.update({"occluded_prob": x, "occluded": hit,
                          "occlusion_tag": (OCCLUSION_TAGS[0] if v.get("cto") else OCCLUSION_TAGS[1]) if hit else ""})
        # Possible stent (procedure head): an annotation only; no flag, threshold or shown % changes.
        if "stent_probability" in res and "stent_annotation" in thresholds:
            cut_stent = thresholds["stent_annotation"]
            out["stent_annotation"] = {"threshold": cut_stent}
            for g, x in zip(groups, res["stent_probability"].float().cpu().tolist()):
                x = round(float(x), PROBABILITY_DECIMALS)
                out["segments"][g].update({"stent_prob": x, "possible_stent": bool(x > cut_stent)})
        if "lad_physiology" in out and "occluded_probability" in res:  # v19 results name the measurement region
            CustomPredictionService.add_pressure_region(out)
        unsupported = [g for g in groups if g in set(unsupported)]
        if unsupported:
            out["unsupported_segments"] = unsupported
            for g in unsupported:
                v = out["segments"][g]
                v.update({k: False for k in ("significant", *LESION_NAMES, "occluded", "possible_stent") if k in v})
                if "occlusion_tag" in v:
                    v["occlusion_tag"] = ""
                v["unsupported"] = True
        # display only: stenosis_pct and the flags are unchanged
        display_calibration.add_display(out["segments"], model_version, skip=unsupported)
        return out

    @staticmethod
    def add_pressure_region(preds: dict) -> None:
        """The LAD FFR/iFR region: where pressure was measured in the training labels (proximal and mid LAD), not a
        localisation of the culprit lesion by the model. Copies the two segments' stenosis readings next to it."""
        ph, segs = preds["lad_physiology"], preds["segments"]
        ph["region"] = list(PRESSURE_REGION)
        ph["region_label"] = PRESSURE_REGION_LABEL
        ph["region_segments"] = {g: {"stenosis_pct": segs[g]["stenosis_pct"], "prob_significant": segs[g]["prob_significant"],
                                     "significant": segs[g]["significant"]} for g in PRESSURE_REGION if g in segs}

    # -- side check ------------------------------------------------------------

    @staticmethod
    def coronary_views(request: PredictRequest) -> Optional[dict]:
        """`additionalMetadata.coronaryViews`: [{"sopInstanceUID", "viewLabel"}, ...], the view label of each run sent
        (angiogram procedure classifier). Absent -> None (no side check); malformed -> error."""
        views = (getattr(request, "additionalMetadata", None) or {}).get("coronaryViews")
        if views is None:
            return None
        if not isinstance(views, list) or not views or len(views) > MAX_VIEW_ENTRIES:
            raise ValueError(f"coronaryViews must be a list of 1 to {MAX_VIEW_ENTRIES} entries")
        out = {}
        for item in views:
            if (not isinstance(item, dict) or not isinstance(item.get("sopInstanceUID"), str)
                    or not item["sopInstanceUID"] or len(item["sopInstanceUID"]) > 128
                    or not (item.get("viewLabel") is None or isinstance(item["viewLabel"], str))):
                raise ValueError("coronaryViews entries need a sopInstanceUID and a viewLabel (string or null)")
            out[item["sopInstanceUID"]] = item.get("viewLabel")
        return out

    @staticmethod
    def apply_side_check(preds: dict, views: Optional[dict], used_sops: list, dominance: Optional[dict]) -> None:
        """Marks the segments of a coronary side with no run among the runs read as not assessed: their numbers stay
        as the model wrote them, but `assessed` is false, every flag and annotation is false, they get no display
        fields and are left out of the report and the diagnosis. The PDA and posterolateral branch follow the
        dominance (the caller's, else the model's own estimate). LAD physiology is not assessed without a left run.
        Without view labels for every run read, nothing changes and `side_check.available` is false."""
        labels = [views.get(sop) for sop in used_sops] if views is not None else []
        sides = [SIDE_VIEW_LABELS.get(label) for label in labels]
        if views is None or not used_sops or None in sides:
            preds["side_check"] = {"available": False,
                                   "reason": "view labels not provided" if views is None else
                                             "a run read has no left/right coronary view label"}
            return
        left, right = sides.count("left"), sides.count("right")
        missing = "right" if not right else "left" if not left else None
        if dominance:
            dom_call, dom_source = dominance["call"], "angiogram classifier"
        elif preds.get("model_dominance"):
            dom_call = "left" if preds["model_dominance"]["dominance_left_pred"] else "right"
            dom_source = "model estimate"
        else:
            dom_call, dom_source = "right", "default (right dominant)"
        check = {"available": True, "left_runs": left, "right_runs": right, "not_assessed_side": missing}
        if missing:
            check["dominance_side"] = {"call": dom_call, "source": dom_source}
            side_of = {**{g: "right" for g in RIGHT_SEGMENTS}, **{g: "left" for g in LEFT_SEGMENTS},
                       **{g: dom_call for g in DOMINANCE_SEGMENTS}}
            off = [g for g in preds["segments"] if side_of.get(g) == missing]
            for g in off:
                v = preds["segments"][g]
                v.update({k: False for k in ("significant", *LESION_NAMES, "occluded", "possible_stent") if k in v})
                if "occlusion_tag" in v:
                    v["occlusion_tag"] = ""
                v.pop("display_pct", None)
                v.pop("display_category", None)
                v["assessed"] = False
                v["not_assessed_reason"] = NOT_ASSESSED_REASON[missing]
            check["not_assessed_segments"] = off
            ph = preds.get("lad_physiology")
            if ph is not None and missing == "left":
                ph.update({"ffr_abnormal": False, "ifr_abnormal": False, "assessed": False,
                           "not_assessed_reason": NOT_ASSESSED_REASON["left"]})
            preds["explainability"] = [it for it in preds.get("explainability") or [] if it.get("segment") not in off]
        preds["side_check"] = check

    # -- reporting -------------------------------------------------------------

    @staticmethod
    def significant_segments(preds: dict) -> list[str]:
        return [g for g, v in preds["segments"].items() if v["significant"]]

    def diagnosis_text(self, preds: dict) -> str:
        # The report leaves out HIDDEN_SEGMENTS (posterior LV branch) and segments not assessed (side check). A segment
        # shown occluded (v19: occluded flag; v13/v15: CTO flag) is reported as 100%.
        version = display_calibration.result_model_version(preds)
        hidden = HIDDEN_SEGMENTS | set(preds.get("unsupported_segments") or ()) | not_assessed(preds)
        segs = {g: display_calibration.with_display(v, version) for g, v in preds["segments"].items() if g not in hidden}
        occluded = lambda v: v["display_category"] == "occluded"
        sig = [g for g, v in segs.items() if v["significant"] or occluded(v)]
        shown_pct = lambda v: v["display_pct"]
        stent = lambda v: " (possible stent: grade may be unreliable after PCI)" if v.get("possible_stent") else ""

        def pct(v):
            if v.get("occluded"):
                return (f"shown 100% (occluded) - {v.get('occlusion_tag') or 'acute/other'}, P(occluded) "
                        f"{shown(v['occluded_prob'], preds['occluded_threshold']['threshold'], 2)}")
            if occluded(v):
                return f"shown 100%, CTO flag; model estimate {v['stenosis_pct']:.0f}%"
            return f"shown {shown_pct(v)}%, model estimate {v['stenosis_pct']:.0f}%"
        text = (f"Flagged for possible significant stenosis{' or occlusion' if 'occluded_threshold' in preds else ''} "
                "(probability above its validation threshold): "
                + ", ".join(f"{SEGMENT_NAMES[g]} (P {shown(segs[g]['prob_significant'], segs[g]['prob_threshold'], 3)}, "
                            f"{pct(segs[g])}){stent(segs[g])}" for g in sig) + "."
                if sig else "No segment flagged for significant stenosis.")
        others = [g for g in segs if g not in sig]
        marked = [g for g in others if shown_pct(segs[g]) >= 30]
        if marked:
            text += (" Other segments with a shown stenosis of 30% or more: "
                     + ", ".join(f"{SEGMENT_NAMES[g]} ({shown_pct(segs[g])}%){stent(segs[g])}" for g in marked) + ".")
        rest = [g for g in others if g not in marked and segs[g].get("possible_stent")]
        if rest:
            text += (" Possible stent, grade may be unreliable after PCI: "
                     + ", ".join(f"{SEGMENT_NAMES[g]} ({shown_pct(segs[g])}%)" for g in rest) + ".")
        normal = sum(shown_pct(v) == 0 for v in segs.values())
        text += (f" {normal} of {len(segs)} segments shown as normal (model estimate below "
                 f"{display_calibration.calibration(version)['zero_below_pct']:g}%).")
        check = preds.get("side_check") or {}
        if check.get("not_assessed_side"):
            side = check["not_assessed_side"]
            text += (f" Not assessed ({NOT_ASSESSED_REASON[side]} among the {check['left_runs'] + check['right_runs']} "
                     f"runs read): " + ", ".join(SEGMENT_NAMES[g] for g in check["not_assessed_segments"] if g not in HIDDEN_SEGMENTS)
                     + "; their values are not reported.")
        if "lad_physiology" in preds:
            p = preds["lad_physiology"]
            if p.get("assessed") is False:
                text += f" LAD physiology: not assessed ({p['not_assessed_reason']})."
            else:
                call = lambda k: "abnormal" if p[f"{k}_abnormal"] else "normal"
                prob = lambda k: shown(p[f"{k}_abnormal_prob"], p[f"{k}_abnormal_threshold"], 2)
                text += (f" LAD physiology{' (proximal-mid LAD)' if 'region' in p else ''}: FFR predicted {call('ffr')} "
                         f"(P(FFR ≤ 0.80) {prob('ffr')}, threshold {p['ffr_abnormal_threshold']:.4f}); iFR predicted "
                         f"{call('ifr')} (P(iFR ≤ 0.89) {prob('ifr')}, threshold {p['ifr_abnormal_threshold']:.4f}).")
                if "region" in p and (p["ffr_abnormal"] or p["ifr_abnormal"]):
                    text += " " + pressure_region_sentence(p, segs)
        for name, label in LESION_NAMES.items() if "lesion_thresholds" in preds else ():
            hit = [g for g, v in segs.items() if v[name]]
            cut = preds["lesion_thresholds"][name]
            text += (f" Flagged for {label} (P above {cut:.4f}): "
                     + ", ".join(f"{SEGMENT_NAMES[g]} (P {shown(segs[g][f'{name}_prob'], cut, 2)})" for g in hit) + "."
                     if hit else f" No segment flagged for {label}.")
        if study := preds.get("study_lesions"):
            text += (" Study-level probability, any segment (no yes/no call): "
                     + ", ".join(f"{label} {study[f'study_{name}_prob']:.2f}" for name, label in LESION_NAMES.items()
                                 if f"study_{name}_prob" in study) + ".")
        if own := preds.get("model_dominance"):
            text += (f" Model estimate of the dominance: {'left' if own['dominance_left_pred'] else 'right'} (P(left) "
                     f"{shown(own['dominance_left_prob'], own['dominance_left_threshold'], 3)}, threshold "
                     f"{own['dominance_left_threshold']:.4f}; trained on model-derived labels, not used to draw the schema).")
        if dom := preds.get("coronary_dominance"):
            text += (f" Coronary dominance: {dom['call']} (angiogram classifier, "
                     + (f"majority of {dom['video_count']} diagnostic videos: {dom['left_votes']} left, {dom['right_votes']} right)."
                        if dom["method"] == "majority" else
                        f"tied {dom['left_votes']}/{dom['right_votes']}, mean P(left) {dom['mean_left_probability']:.2f}).")
                     )
        return text

    @staticmethod
    def recommendations(preds: dict) -> dict:
        version = display_calibration.result_model_version(preds)
        auroc_en, auroc_fr = PRESSURE_TEST_AUROC.get(version, ("", ""))
        auroc_en, auroc_fr = (f" (test AUROC about {auroc_en})" if auroc_en else "",
                              f" (AUROC test environ {auroc_fr})" if auroc_fr else "")
        region = "region" in (preds.get("lad_physiology") or {})
        en = ("Research model, not for clinical decisions. Flags use thresholds chosen on the internal validation set, "
              "not a validated clinical operating point. Confirm every reading on the angiogram; LAD physiology "
              f"outputs{auroc_en} do not replace invasive FFR/iFR. Calcification, thrombus and chronic "
              "total occlusion flags are set for few false alarms and miss many of these findings.")
        fr = ("Modèle de recherche, ne pas utiliser pour la décision clinique. Les signalements utilisent des seuils "
              "choisis sur l'ensemble de validation interne, et non un seuil clinique validé. Confirmer chaque lecture sur "
              f"l'angiographie; les sorties de physiologie IVA{auroc_fr} ne remplacent pas la FFR/iFR invasive. "
              "Les signalements de calcification, de thrombus et d'occlusion totale chronique sont réglés pour peu de "
              "fausses alertes et manquent une bonne partie de ces lésions.")
        if region:
            en += (" The predicted FFR/iFR refers to the proximal and mid LAD, where pressure was measured in the "
                   "training data; it does not localise the culprit lesion.")
            fr += (" La FFR/iFR prédite se rapporte à l'IVA proximale et moyenne, où la pression a été mesurée dans les "
                   "données d'entraînement; elle ne localise pas la lésion coupable.")
        if "occluded_threshold" in preds:
            en += (" \"100% (occluded)\" is shown above a probability threshold chosen on validation (about a third of "
                   "occlusions are shown so; the others show their %); the CTO / acute tag is weak.")
            fr += (" « 100% (occlusion) » est affiché au-dessus d'un seuil choisi sur la validation (environ un tiers des "
                   "occlusions; les autres affichent leur %); l'étiquette OTC / aiguë est peu fiable.")
        if "stent_annotation" in preds:
            en += (" Post-PCI runs (stented segments) are read less reliably; do not upload them with diagnostic runs.")
            fr += (" Les injections post-ICP (segments stentés) sont lues de façon moins fiable; ne pas les téléverser avec "
                   "les injections diagnostiques.")
        return {"en": en, "fr": fr, "presentable": True}

    @staticmethod
    def report_model_version(preds: Optional[dict] = None) -> str:
        """Version printed in the report header: the model version and its display calibration ("v15-cal1"). With a
        `predictions` object it is the version of that result ("v13-cal1" for a result stored by 1.1.0 to 1.4.1),
        without one the version of the served model."""
        version = display_calibration.result_model_version(preds) if preds else CustomPredictionService.model_version()
        return display_calibration.label(version)

    @staticmethod
    def model_version() -> str:
        """Model version ("v15"): from the loaded checkpoint structure, else from the model_config.json downloaded
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
        return render_report(preds, cls.report_model_version(preds), cls.recommendations(preds)["en"])

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
        views = self.coronary_views(request)
        preds = self._run_inference(self._decode_dicoms(request))
        if preds is None:
            return None
        used_sops = preds.pop("_used_sops", [])
        if dominance is not None:
            preds["coronary_dominance"] = dominance
        if "occluded_threshold" in preds:  # v19 on: results of this container carry a side check
            self.apply_side_check(preds, views, used_sops, dominance)
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
