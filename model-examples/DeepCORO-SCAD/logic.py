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
    DEFAULT_DATASET_MEAN = [98.33860778808594, 98.33860778808594, 98.33860778808594]
    DEFAULT_DATASET_STD = [33.891441345214844, 33.891441345214844, 33.891441345214844]
    PRIMARY_HEAD = "scad"

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
        print(f"DeepCORO-SCAD loaded. CUDA: {torch.cuda.is_available()}")

    # -- post-processing -------------------------------------------------

    def _postprocess(self, outputs: dict[str, torch.Tensor]) -> dict[str, float]:
        """Convert every checkpoint head to its output scale.

        Binary heads use a sigmoid; the auxiliary stenosis regression head is clamped to
        its training range. Only the ``scad`` head is reported.
        """
        result: dict[str, float] = {}
        for key, value in outputs.items():
            spec = CustomPredictionService._class_mapping.get(key)
            if spec is None:
                raise ValueError(f"Unknown checkpoint head: {key}")
            if spec["head_dim"] != 1:
                raise ValueError(f"Unsupported DeepCORO-SCAD head configuration: {key}")
            logit = value.detach().float().reshape(-1)
            if logit.numel() != 1:
                raise ValueError(f"Expected one output for {key}, received {logit.numel()}")
            if spec["task"] == "binary_classification":
                result[key] = float(torch.sigmoid(logit[0]))
            elif spec["task"] == "regression":
                result[key] = float(
                    torch.clamp(logit[0], float(spec["min"]), float(spec["max"]))
                )
            else:
                raise ValueError(f"Unsupported DeepCORO-SCAD head task: {key}")
        if self.PRIMARY_HEAD not in result:
            raise ValueError("The checkpoint did not return the scad head")
        return result

    # -- clinical formatting --------------------------------------------

    def _threshold(self) -> float:
        return float(CustomPredictionService._class_mapping[self.PRIMARY_HEAD]["threshold"])

    def _is_suspected(self, preds: dict[str, float]) -> bool:
        return bool(preds[self.PRIMARY_HEAD] >= self._threshold())

    def _format_predictions(self, preds: dict[str, float]) -> dict[str, object]:
        spec = CustomPredictionService._class_mapping[self.PRIMARY_HEAD]
        probability = preds[self.PRIMARY_HEAD]
        return {
            "scad": {
                "name": spec["name"],
                "probability": round(float(probability), 4),
                "threshold": self._threshold(),
                "scadSuspected": self._is_suspected(preds),
            },
            "thresholdNote": (
                "The threshold was fixed on the validation set to favour sensitivity. "
                "The probability is a model output, not a calibrated clinical risk."
            ),
        }

    def _diagnosis_text(self, preds: dict[str, float]) -> str:
        probability = preds[self.PRIMARY_HEAD]
        status = "SCAD suspected" if self._is_suspected(preds) else "SCAD not suspected"
        return (
            f"DeepCORO-SCAD: {status} (probability {probability:.3f}, "
            f"threshold {self._threshold():.4f}) | research use only, not a clinical diagnosis"
        )

    def _recommendations(self, preds: dict[str, float]) -> dict[str, object]:
        probability = preds[self.PRIMARY_HEAD]
        if self._is_suspected(preds):
            en = (
                f"<strong>SCAD suspected (probability {probability:.3f}).</strong> "
                "The model output is above the research threshold. Review the angiogram for "
                "spontaneous coronary artery dissection; the model does not localise the "
                "lesion. Research use only: do not change management on the basis of this "
                "output."
            )
            fr = (
                f"<strong>DCS suspectée (probabilité {probability:.3f}).</strong> "
                "La sortie du modèle dépasse le seuil de recherche. Réviser la coronarographie "
                "à la recherche d'une dissection coronarienne spontanée ; le modèle ne localise "
                "pas la lésion. Usage en recherche seulement : ne pas modifier la prise en "
                "charge sur la base de ce résultat."
            )
        else:
            en = (
                f"<strong>SCAD not suspected (probability {probability:.3f}).</strong> "
                "The model output is below the research threshold. A result below the threshold "
                "does not exclude SCAD; interpret the angiogram clinically. Research use only."
            )
            fr = (
                f"<strong>DCS non suspectée (probabilité {probability:.3f}).</strong> "
                "La sortie du modèle est sous le seuil de recherche. Un résultat sous le seuil "
                "n'exclut pas une DCS ; interpréter la coronarographie cliniquement. Usage en "
                "recherche seulement."
            )
        return {"en": en, "fr": fr, "presentable": True}

    # -- DICOM filtering and selection ----------------------------------

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

    def _order_dicoms(
        self, dicoms: list[pydicom.Dataset], metadata: Optional[dict] = None
    ) -> list[pydicom.Dataset]:
        """Order candidate videos; the first ``num_videos`` processable ones are used.

        Videos are ordered by SeriesTime. When per-series metadata is available, left and
        right coronary views are interleaved (L, R, L, R, ...) so that a truncated study
        keeps both territories, as in the training data where each study's first four
        clips alternate left and right coronary views.
        """
        ordered = sorted(dicoms, key=self._extract_series_time)
        if not metadata:
            return ordered

        def structure(dicom: pydicom.Dataset) -> Optional[str]:
            meta = metadata.get(str(getattr(dicom, "SeriesInstanceUID", ""))) or {}
            return meta.get("main_structure")

        left = [d for d in ordered if structure(d) == "Left Coronary"]
        right = [d for d in ordered if structure(d) == "Right Coronary"]
        others = [d for d in ordered if structure(d) not in ("Left Coronary", "Right Coronary")]
        interleaved: list[pydicom.Dataset] = []
        for index in range(max(len(left), len(right))):
            if index < len(left):
                interleaved.append(left[index])
            if index < len(right):
                interleaved.append(right[index])
        return interleaved + others

    # -- HTML report -----------------------------------------------------

    def _render_html(self, preds: dict[str, float], recs: dict[str, object]) -> str:
        probability = preds[self.PRIMARY_HEAD]
        threshold = self._threshold()
        suspected = self._is_suspected(preds)
        status_en = "SCAD suspected" if suspected else "SCAD not suspected"
        status_fr = "DCS suspectée" if suspected else "DCS non suspectée"
        status_class = "flag-pos" if suspected else "flag-neg"
        en = recs.get("en", "")
        fr = recs.get("fr", "")
        return f"""<!DOCTYPE html>
<html><head><meta charset=\"utf-8\"><title>DeepCORO-SCAD Report</title>
<style>
 body{{font-family:Segoe UI,Arial,sans-serif;background:#f8f9fa;color:#23303f;padding:24px;}}
 .card{{max-width:900px;margin:0 auto;background:#fff;border-radius:12px;padding:28px;box-shadow:0 4px 12px rgba(0,0,0,.09);}}
 h1{{margin:0 0 6px;font-size:24px}} h2{{font-size:17px;margin:24px 0 8px}}
 .subtitle{{color:#687482;margin-bottom:18px}} .score{{background:#eef5ff;border-radius:8px;padding:18px;margin-bottom:16px}}
 .score strong{{font-size:40px;display:block}} table{{width:100%;border-collapse:collapse}}
 th,td{{padding:9px;border-bottom:1px solid #e3e7eb;text-align:left}} th{{background:#f4f6f8}}
 .flag-pos{{color:#b03a2e;font-weight:600}} .flag-neg{{color:#1e8449;font-weight:600}}
 .rec{{background:#eaf4ff;border-left:4px solid #3498db;padding:14px 18px;border-radius:6px;margin-top:20px}}
 .rec p{{margin:8px 0}} .translation{{display:block;color:#526171;font-size:.94em;margin-top:2px}}
 .warn{{background:#fff4e5;border-left:4px solid #e67e22;padding:14px 18px;border-radius:6px;margin-top:16px}}
 .warn p{{margin:8px 0}}
</style></head><body><div class=\"card\">
 <h1>DeepCORO-SCAD</h1>
 <div class=\"subtitle\"><span lang=\"en\">Spontaneous coronary artery dissection (SCAD) research score from coronary angiography</span>
 <span class=\"translation\" lang=\"fr\">Score de recherche de dissection coronarienne spontanée (DCS) dérivé de la coronarographie</span></div>
 <div class=\"score\"><strong>{probability:.3f}</strong><span lang=\"en\">SCAD probability (model output)</span>
 <span class=\"translation\" lang=\"fr\">Probabilité de DCS (sortie du modèle)</span>
 <div class=\"{status_class}\"><span lang=\"en\">{status_en}</span> / <span lang=\"fr\">{status_fr}</span></div></div>
 <table><tr><th>Output / Sortie</th><th>Probability / Probabilité</th><th>Threshold / Seuil</th><th>SCAD suspected / DCS suspectée</th></tr>
 <tr><td><span lang=\"en\">Spontaneous coronary artery dissection</span><span class=\"translation\" lang=\"fr\">Dissection coronarienne spontanée</span></td>
 <td>{probability:.3f}</td><td>{threshold:.4f}</td>
 <td><span lang=\"en\">{'Yes' if suspected else 'No'}</span> / <span lang=\"fr\">{'Oui' if suspected else 'Non'}</span></td></tr></table>
 <div class=\"rec\"><h2>Research recommendation / Recommandation de recherche</h2>
 <p lang=\"en\">{en}</p><p lang=\"fr\">{fr}</p></div>
 <div class=\"warn\"><p lang=\"en\"><strong>Research use only — not for clinical decision-making.</strong>
 The probability is a model output, not a calibrated clinical risk. The threshold ({threshold:.4f}) was fixed
 on the validation set to favour sensitivity. On the held-out Montreal Heart Institute test set the model
 reached an AUROC of 0.87 with a sensitivity of 0.69 at this threshold, so a negative result does not exclude
 SCAD. External sites contributed SCAD cases only, which can inflate multi-site performance. The model does
 not localise the lesion. No regulatory clearance.</p>
 <p lang=\"fr\"><strong>Usage en recherche seulement — non destiné à la prise de décision clinique.</strong>
 La probabilité est une sortie du modèle et non un risque clinique étalonné. Le seuil ({threshold:.4f}) a été
 fixé sur l'ensemble de validation afin de privilégier la sensibilité. Sur l'ensemble de test de l'Institut de
 Cardiologie de Montréal, le modèle a obtenu une ASC de 0,87 et une sensibilité de 0,69 à ce seuil ; un résultat
 négatif n'exclut donc pas une DCS. Les sites externes n'ont fourni que des cas de DCS, ce qui peut surestimer la
 performance multisite. Le modèle ne localise pas la lésion. Aucune autorisation réglementaire.</p></div>
</div></body></html>"""

    # -- handlers --------------------------------------------------------

    async def _handle_html_output(self, request: PredictRequest):
        dicoms = self._decode_dicoms(request)
        metadata = request.additionalMetadata or None
        if metadata:
            dicoms = self._filter_dicoms_with_metadata(dicoms, metadata)

        outputs = self._run_inference(dicoms, metadata)
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
        metadata = request.additionalMetadata or None
        if metadata:
            dicoms = self._filter_dicoms_with_metadata(dicoms, metadata)

        outputs = self._run_inference(dicoms, metadata)
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
        self, dicoms: list[pydicom.Dataset], metadata: Optional[dict] = None
    ) -> Optional[dict[str, torch.Tensor]]:
        try:
            videos: list[np.ndarray] = []
            max_videos = CustomPredictionService.model_config["VideoMILWrapper"]["num_videos"]
            _, resize = self._get_video_loading_config()

            dicoms = self._order_dicoms(dicoms, metadata)
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


def _to_display_uint8(video: np.ndarray, photometrics: str) -> np.ndarray:
    """Match the DeepCORO-SCAD training conversion of non-8-bit and MONOCHROME1 DICOMs.

    The SCAD training videos from external sites were written from DICOM with a clip-level
    0.5-99.5 percentile window to uint8 when the pixel data was not already 8-bit, and
    MONOCHROME1 clips were inverted. Without this step 10/12-bit XA studies reach the
    video writer as uint16 and are scored on corrupted frames. 8-bit MONOCHROME2 clips,
    the common case, pass through unchanged.
    """
    if video.dtype != np.uint8:
        video = video.astype(np.float32)
        low, high = np.percentile(video, 0.5), np.percentile(video, 99.5)
        video = (np.clip((video - low) / max(high - low, 1e-6), 0, 1) * 255).astype(np.uint8)
    if photometrics == "MONOCHROME1":
        video = 255 - video
    return video


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
        video = _to_display_uint8(video, photometrics)
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
