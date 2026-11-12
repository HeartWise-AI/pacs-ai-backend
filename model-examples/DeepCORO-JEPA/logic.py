"""DeepCORO-JEPA: one EchoJEPA video encoder that segments the coronary tree itself and reads 18-segment
stenosis (+ LAD FFR/iFR, + lesion types when the checkpoint has those heads) from a diagnostic angiography study.

Not a CLIP/MIL model: there is no zero-video padding. The model takes the real videos as a set and runs the encoder
at most twice per study (full views at 224 px, then tracked 320 px crops per segment); `measured_forward` enforces
the two-pass budget. Yes/no calls use the thresholds.json shipped with the pinned model revision (chosen on the
validation set): a finding is positive when its probability is strictly greater than its threshold. Research use only.
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
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, PredictRequest

MODELS_DIR = Path(__file__).resolve().parent / "models"
sys.path.insert(0, str(MODELS_DIR / "deepcoro_model"))

SERIES_TIME_TAG = (0x0008, 0x0031)
FPS_TAG = (0x0008, 0x2144)

SEGMENT_NAMES = {
    "prox_rca": "Proximal RCA", "mid_rca": "Mid RCA", "dist_rca": "Distal RCA", "pda": "PDA",
    "posterolateral": "Posterolateral", "left_main": "Left main", "prox_lad": "Proximal LAD", "mid_lad": "Mid LAD",
    "dist_lad": "Distal LAD", "D1": "First diagonal", "D2": "Second diagonal", "prox_lcx": "Proximal LCx",
    "mid_lcx": "Mid LCx", "dist_lcx": "Distal LCx", "om1": "First obtuse marginal", "om2": "Second obtuse marginal",
    "bx": "Ramus (bisector)", "lvp": "Posterior left ventricular",
}
PRESSURE_OPERATING_POINTS = ("balanced", "high_sensitivity", "high_specificity")


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
            bool(structure.get("physiology")), CustomPredictionService.get_model_info()["provenance"]["weightsSha256"])
        device = "cuda" if torch.cuda.is_available() else "cpu"
        model = multilayer_model_from_payload(payload, device=device, train_blocks=0)
        model.eval()
        CustomPredictionService.models["deepcoro_jepa"] = model
        CustomPredictionService.structure = structure
        CustomPredictionService.is_initialized = True
        print(f"DeepCORO-JEPA {structure.get('model_version')} loaded on {device}; heads {structure.get('heads')}")

    @staticmethod
    def load_thresholds(models_dir, settings: dict, groups, physiology: bool, weights_sha256: str) -> dict:
        """Decision thresholds of the pinned model revision, one per output. `settings` is the "Thresholds" block of
        models/config.json. Fails on an unknown key or operating point, when the file belongs to other weights or when
        an output has no threshold: no silent default."""
        if set(settings) != {"path", "pressure_operating_point"}:
            raise ValueError(f'"Thresholds" takes exactly "path" and "pressure_operating_point", got {sorted(settings)}')
        pressure_operating_point = settings["pressure_operating_point"]
        if pressure_operating_point not in PRESSURE_OPERATING_POINTS:
            raise ValueError(f"Unknown pressure_operating_point {pressure_operating_point!r}; choose one of {PRESSURE_OPERATING_POINTS}")
        path = Path(models_dir) / settings["path"]
        spec = json.loads(path.read_text(encoding="utf-8"))
        if spec["weights_sha256"] != weights_sha256:
            raise ValueError(f"{path} was derived for weights {spec['weights_sha256']}, not {weights_sha256}")
        wanted = {f"{g}_prob_significant": "balanced" for g in groups}
        if physiology:
            wanted.update({"ffr_abnormal_prob": pressure_operating_point, "ifr_abnormal_prob": pressure_operating_point})
        missing = [name for name, point in wanted.items() if point not in spec["thresholds"].get(name, {})]
        if missing:
            raise ValueError(f"{path} has no threshold for {missing}")
        out = {name: float(spec["thresholds"][name][point]) for name, point in wanted.items()}
        if any(not 0.0 < t < 1.0 for t in out.values()):
            raise ValueError(f"{path} has a threshold outside (0, 1)")
        return {"pressure_operating_point": pressure_operating_point, "values": out}

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
        clips = []
        with tempfile.TemporaryDirectory() as tmp:
            for i, dicom in enumerate(self.select_videos(dicoms)):
                path = self.dicom_to_avi(dicom, os.path.join(tmp, f"{i:02d}.avi"))
                if path is None:
                    continue
                try:
                    clips.append(load_video_clip(path, sampling=cfg["frame_sampling"]))
                except Exception as e:
                    print(f"load_video_clip failed: {e}")
        if not clips:
            return None
        try:
            with torch.inference_mode():
                res = measured_forward(model, clips, args)
        except Exception:
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            raise  # predict() answers 500 PROCESSING_ERROR; None is kept for "no usable clip"
        return self.postprocess(model.groups, res, len(clips), CustomPredictionService.thresholds)

    @staticmethod
    def postprocess(groups, res: dict, n_videos: int, thresholds: dict) -> dict:
        """`thresholds` comes from load_thresholds. A call is positive when the probability is strictly greater than
        its threshold, taken on the unrounded probability."""
        cut = thresholds["values"]
        out = {"n_videos": n_videos, "encoder_passes": int(res["encoder_passes"]), "segments": {}}
        sev = res["severity"].float().cpu().tolist()
        prob = res["binary_probability"].float().cpu().tolist()
        for i, g in enumerate(groups):
            out["segments"][g] = {"stenosis_pct": round(min(max(sev[i], 0.0), 100.0), 1),
                                  "prob_significant": round(prob[i], 4),
                                  "prob_threshold": cut[f"{g}_prob_significant"],
                                  "significant": bool(prob[i] > cut[f"{g}_prob_significant"]),
                                  "significant_threshold_pct": 50 if g == "left_main" else 70}
        if "physiology" in res:
            ph = {k: float(v.float().cpu().reshape(-1)[0]) for k, v in res["physiology"].items()}
            prob_abnormal = {k: float(1 / (1 + np.exp(-ph[f"{k}_logit"]))) for k in ("ffr", "ifr")}
            out["lad_physiology"] = {"ffr": round(ph["ffr"], 3), "ifr": round(ph["ifr"], 3),
                                     "operating_point": thresholds["pressure_operating_point"]}
            for k in ("ffr", "ifr"):
                out["lad_physiology"].update({f"{k}_abnormal_prob": round(prob_abnormal[k], 4),
                                              f"{k}_abnormal_threshold": cut[f"{k}_abnormal_prob"],
                                              f"{k}_abnormal": bool(prob_abnormal[k] > cut[f"{k}_abnormal_prob"])})
        for name in ("calcif", "thrombus", "cto"):
            if f"lesion_{name}_probability" in res:
                for g, x in zip(groups, res[f"lesion_{name}_probability"].float().cpu().tolist()):
                    out["segments"][g][f"{name}_prob"] = round(float(x), 4)
        return out

    # -- reporting -------------------------------------------------------------

    @staticmethod
    def significant_segments(preds: dict) -> list[str]:
        return [g for g, v in preds["segments"].items() if v["significant"]]

    def diagnosis_text(self, preds: dict) -> str:
        sig = self.significant_segments(preds)
        text = ("Flagged for possible significant stenosis (probability above its validation threshold): "
                + ", ".join(f"{SEGMENT_NAMES[g]} (P {preds['segments'][g]['prob_significant']:.3f}, estimated "
                            f"{preds['segments'][g]['stenosis_pct']:.0f}%)" for g in sig) + "."
                if sig else "No segment flagged for significant stenosis.")
        if "lad_physiology" in preds:
            p = preds["lad_physiology"]
            call = lambda k: "abnormal" if p[f"{k}_abnormal"] else "normal"
            text += (f" LAD physiology: FFR predicted {call('ffr')} (P(FFR ≤ 0.80) {p['ffr_abnormal_prob']:.2f}, threshold "
                     f"{p['ffr_abnormal_threshold']:.2f}); iFR predicted {call('ifr')} (P(iFR ≤ 0.89) "
                     f"{p['ifr_abnormal_prob']:.2f}, threshold {p['ifr_abnormal_threshold']:.2f}).")
        return text

    @staticmethod
    def recommendations(preds: dict) -> dict:
        en = ("Research model, not for clinical decisions. Flags use thresholds chosen on the internal validation set, "
              "not a validated clinical operating point. Confirm every reading on the angiogram; LAD physiology "
              "outputs (test AUROC about 0.72) do not replace invasive FFR/iFR.")
        fr = ("Modèle de recherche, ne pas utiliser pour la décision clinique. Les signalements utilisent des seuils "
              "choisis sur l'ensemble de validation interne, et non un seuil clinique validé. Confirmer chaque lecture sur "
              "l'angiographie; les sorties de physiologie IVA (AUROC test environ 0,72) ne remplacent pas la FFR/iFR invasive.")
        return {"en": en, "fr": fr, "presentable": True}

    def render_html(self, preds: dict) -> str:
        rows = "".join(
            f"<tr><td>{SEGMENT_NAMES[g]}</td><td style='text-align:right'>{v['stenosis_pct']:.0f}%</td>"
            f"<td style='text-align:right'>{v['prob_significant']:.3f}</td>"
            f"<td style='text-align:right'>{v['prob_threshold']:.4f}</td><td>{'Yes' if v['significant'] else 'No'}</td>"
            + "".join(f"<td style='text-align:right'>{v[k]:.2f}</td>" for k in ("calcif_prob", "thrombus_prob", "cto_prob") if k in v)
            + "</tr>" for g, v in preds["segments"].items())
        lesion_cols = "".join(f"<th>{h}</th>" for k, h in (("calcif_prob", "P(calcification)"), ("thrombus_prob", "P(thrombus)"), ("cto_prob", "P(CTO)"))
                              if k in next(iter(preds["segments"].values())))
        phys = ""
        if "lad_physiology" in preds:
            p = preds["lad_physiology"]
            call = lambda k: "abnormal" if p[f"{k}_abnormal"] else "normal"
            phys = (f"<h3>LAD physiology</h3><p>FFR predicted <b>{call('ffr')}</b>: P(FFR ≤ 0.80) {p['ffr_abnormal_prob']:.2f}, "
                    f"threshold {p['ffr_abnormal_threshold']:.2f}, estimated value {p['ffr']:.2f}.<br>"
                    f"iFR predicted <b>{call('ifr')}</b>: P(iFR ≤ 0.89) {p['ifr_abnormal_prob']:.2f}, "
                    f"threshold {p['ifr_abnormal_threshold']:.2f}, estimated value {p['ifr']:.2f}.<br>"
                    f"Operating point: {p['operating_point']}.</p>")
        return ("<html><body style='font-family:sans-serif'>"
                f"<h2>DeepCORO-JEPA {CustomPredictionService.structure.get('model_version', '')}</h2>"
                f"<p>{self.diagnosis_text(preds)}</p>"
                f"<table border='1' cellspacing='0' cellpadding='4'><tr><th>Segment</th><th>Stenosis</th><th>P(significant)</th><th>Threshold</th><th>Flagged</th>{lesion_cols}</tr>{rows}</table>"
                f"{phys}<p style='color:#555'>{preds['n_videos']} videos, {preds['encoder_passes']} encoder passes. "
                f"{self.recommendations(preds)['en']}</p></body></html>")

    async def _handle_json_output(self, request: PredictRequest):
        preds = self._run_inference(self._decode_dicoms(request))
        if preds is None:
            return {"diagnosis": "No video could be extracted from the current DICOM series", "predictions": {},
                    "modelRecommendations": {"en": "No video could be extracted or processed.",
                                             "fr": "Aucune vidéo n'a pu être extraite ou traitée.", "presentable": False}}
        return {"diagnosis": self.diagnosis_text(preds), "predictions": preds, "modelRecommendations": self.recommendations(preds)}

    async def _handle_html_output(self, request: PredictRequest):
        preds = self._run_inference(self._decode_dicoms(request))
        if preds is None:
            html = "<h1>No video could be extracted from the current DICOM series</h1>"
        else:
            html = self.render_html(preds)
        return {"htmlBase64": base64.b64encode(html.encode("utf-8")).decode("utf-8")}
