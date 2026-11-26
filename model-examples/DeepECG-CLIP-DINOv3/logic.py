import base64
import json
import os
from dataclasses import dataclass
from io import BytesIO
from pathlib import Path
from typing import Optional

import pydicom
import torch
from models.dinov3_heads import SharedTrunkClassifiers, load_state_dict
from models.probes import Probe, discover_probes
from PIL import Image
from report_input import NO_ECG_HTML, NO_ECG_RESULT, difference, fail, member, number, unwrap
from torchvision.transforms import v2
from utils.ecg_inputs import EcgInputError, EcgPage, ecg_page, order_candidates
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, JsonPredictionResponse, PredictRequest

MODEL_DIRECTORY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "models")
PRIMARY_HEAD = "aco"
SECONDARY_HEAD = "acco_vs_aico"
DECIMALS = 4
SOURCE_LABELS = {
    "waveform": {
        "en": "DICOM 12-lead waveform, drawn with the training renderer",
        "fr": "Tracé DICOM 12 dérivations, dessiné avec le moteur de rendu de l'entraînement",
    },
    "pdf": {
        "en": "Vector ECG PDF, traces redrawn with the training renderer",
        "fr": "ECG PDF vectoriel, tracés redessinés avec le moteur de rendu de l'entraînement",
    },
    "image": {
        "en": "ECG image used as is (not the training layout; least validated input)",
        "fr": "Image d'ECG utilisée telle quelle (hors format d'entraînement ; entrée la moins validée)",
    },
}
THRESHOLD_NOTE = (
    "Probabilities are model outputs, not calibrated risks. Each threshold is the Youden point of its "
    "validation set. A head with applicableWhen is only interpreted when that head is positive. "
    "Multiclass heads flag their most probable class."
)


@dataclass(frozen=True)
class HeadSpec:
    """One reported output: a fine-tuned head or one row of a frozen linear probe."""
    id: str
    label: str
    description: str
    task: str                  # binary | multilabel | multiclass
    kind: str                  # fine-tuned | linear-probe
    group: Optional[str]
    threshold: Optional[float]
    applies_when: Optional[str] = None
    probe: Optional[str] = None


def preprocess_page(image: Image.Image, settings: dict) -> torch.Tensor:
    """[3, H, W] float tensor, exactly as DeepECG-PNG utils/image.process_image prepared the
    training pages (color_mode grayscale, stretch resize, no augmentation)."""
    if settings["color_mode"] != "grayscale" or settings["resize_mode"] != "stretch":
        raise ValueError("Only the grayscale, stretch-resize preprocessing of training is implemented")
    pipeline = v2.Compose([
        v2.Resize(tuple(settings["resize"]), antialias=True),
        v2.Grayscale(num_output_channels=3),
        v2.ToImage(),
        v2.ToDtype(torch.float32, scale=True),
    ])
    tensor = pipeline(image.convert("L"))
    return v2.Normalize(mean=list(settings["mean"]), std=list(settings["std"]))(tensor)


def _read_json(name: str) -> dict:
    with open(os.path.join(MODEL_DIRECTORY, name)) as fp:
        return json.load(fp)


def head_registry(probes: list[Probe]) -> list[HeadSpec]:
    """Every output of the service, in report order: the fine-tuned heads of class_mapping.json,
    then the rows of each probe (sorted by probe id)."""
    specs = [
        HeadSpec(head, spec["name"], spec.get("description", ""), "binary", "fine-tuned", spec.get("group"),
                 float(spec["threshold"]), spec.get("applies_when"))
        for head, spec in _read_json("class_mapping.json").items()
    ]
    for probe in probes:
        for output in probe.outputs:
            specs.append(HeadSpec(output.id, output.label, probe.description, probe.task, "linear-probe",
                                  probe.group or probe.id, output.threshold, None, probe.id))
    ids = [s.id for s in specs]
    if len(set(ids)) != len(ids):
        raise ValueError("Two heads share an id")
    known = set(ids)
    for s in specs:
        if s.applies_when is not None and s.applies_when not in known:
            raise ValueError(f"{s.id} applies when the unknown head {s.applies_when} is positive")
    return specs


def probe_directory() -> Path:
    return Path(MODEL_DIRECTORY) / _read_json("config.json")["ProbeDirectory"]


class CustomPredictionService(BasePredictionService):
    PRIMARY_HEAD = PRIMARY_HEAD
    probes: list[Probe] = []

    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            return
        cfg = _read_json("config.json")
        CustomPredictionService.model_config = cfg
        heads = list(_read_json("class_mapping.json"))
        if heads != list(cfg["Checkpoints"]):
            raise ValueError("class_mapping.json and config.json name different heads")
        probes = discover_probes(probe_directory())
        model = SharedTrunkClassifiers(
            cfg["Backbone"], heads,
            shared_layers=cfg["SharedTrunk"]["shared_layers"],
            dropout=cfg["SharedTrunk"]["dropout_rate"],
            with_frozen_encoder=bool(probes),
        )
        frozen = None
        if probes:
            entry = cfg["FrozenEncoder"]
            frozen = load_state_dict(os.path.join(MODEL_DIRECTORY, entry["path"]), entry["state_dict_key"])
        model.load_heads({
            head: load_state_dict(os.path.join(MODEL_DIRECTORY, cfg["Checkpoints"][head]["path"]))
            for head in heads
        }, frozen)
        model.eval()
        model.to("cuda" if torch.cuda.is_available() else "cpu")
        CustomPredictionService.probes = probes
        CustomPredictionService._registry = head_registry(probes)
        CustomPredictionService.models["classifiers"] = model
        CustomPredictionService.is_initialized = True
        print(f"DeepECG-CLIP-DINOv3 loaded: {len(heads)} fine-tuned heads, {len(probes)} probes, "
              f"{len(CustomPredictionService._registry)} outputs. CUDA: {torch.cuda.is_available()}")

    def stop_model(self):
        CustomPredictionService.models.pop("classifiers", None)
        CustomPredictionService.probes = []
        CustomPredictionService.is_initialized = False
        if torch.cuda.is_available():
            torch.cuda.empty_cache()

    @classmethod
    def registry(cls) -> list[HeadSpec]:
        """The outputs; without a loaded model (render-report) the probe manifests are read from disk."""
        if not hasattr(cls, "_registry"):
            cls._registry = head_registry(discover_probes(probe_directory(), with_weights=False))
        return cls._registry

    # -- inputs ------------------------------------------------------------

    def _decode_dicoms(self, request: PredictRequest) -> list[pydicom.Dataset]:
        dicoms: list[pydicom.Dataset] = []
        for instances in (request.seriesInstanceImages or {}).values():
            for b64 in instances.values():
                if not self._is_valid_base64(b64):
                    continue
                raw = base64.b64decode(b64)
                if not self._is_valid_dicom(raw):
                    continue
                try:
                    dicoms.append(pydicom.dcmread(BytesIO(raw)))
                except Exception as e:
                    print(f"dcmread failed: {type(e).__name__}")
        return dicoms

    def _select_page(self, dicoms: list[pydicom.Dataset]) -> Optional[EcgPage]:
        """The page of the best readable ECG object of the study (waveform, then PDF, then image;
        earliest acquisition first). One ECG is scored per request."""
        for kind, dicom in order_candidates(dicoms):
            try:
                return ecg_page(kind, dicom)
            except EcgInputError as e:
                print(f"skipped a {kind} object: {e}")
        return None

    def _selected_heads(self, request: PredictRequest) -> list[HeadSpec]:
        """All outputs, or those named in additionalMetadata.heads (head ids or probe ids). A head
        that applies only when another is positive brings that head along."""
        registry = self.registry()
        wanted = (request.additionalMetadata or {}).get("heads")
        if wanted is None:
            return list(registry)
        if not isinstance(wanted, list) or not all(isinstance(w, str) for w in wanted):
            raise ValueError("additionalMetadata.heads must be a list of head ids")
        known = {s.id for s in registry} | {s.probe for s in registry if s.probe}
        unknown = sorted(set(wanted) - known)
        if unknown:
            raise ValueError(f"Unknown head(s): {', '.join(unknown)}")
        chosen = {s.id for s in registry if s.id in wanted or (s.probe and s.probe in wanted)}
        chosen |= {s.applies_when for s in registry if s.id in chosen and s.applies_when}
        return [s for s in registry if s.id in chosen]

    def _run_inference(self, dicoms: list[pydicom.Dataset], heads: list[HeadSpec]) -> Optional[tuple[dict, str]]:
        page = self._select_page(dicoms)
        if page is None:
            return None
        tensor = preprocess_page(page.image, CustomPredictionService.model_config["Preprocessing"])
        model = CustomPredictionService.models["classifiers"]
        device = next(model.parameters()).device
        probes = [p for p in CustomPredictionService.probes if any(h.probe == p.id for h in heads)]
        with torch.no_grad():
            logits, features = model(tensor.unsqueeze(0).to(device), frozen_features=bool(probes))
            outputs = {head: value.detach().cpu() for head, value in logits.items()}
            for probe in probes:
                outputs[probe.id] = probe(features.detach().cpu())
        return outputs, page.source

    # -- post-processing -------------------------------------------------------

    def _postprocess(self, outputs: dict[str, torch.Tensor]) -> dict[str, float]:
        """{output id: probability rounded to DECIMALS}: sigmoid of each fine-tuned logit; probe rows as
        computed. Flags, text and the report all read the rounded value the JSON stores, so a stored result
        holds everything needed to draw its report again."""
        result: dict[str, float] = {}
        probes = {p.id: p for p in CustomPredictionService.probes}
        for key, value in outputs.items():
            if key in probes:
                row = value.detach().to(torch.float64).reshape(-1)
                for output, probability in zip(probes[key].outputs, row.tolist()):
                    result[output.id] = round(float(probability), DECIMALS)
                continue
            logit = value.detach().float().reshape(-1)
            if logit.numel() != 1:
                raise ValueError(f"Expected one output for {key}, received {logit.numel()}")
            result[key] = round(float(torch.sigmoid(logit[0])), DECIMALS)
        return result

    def _positive(self, spec: HeadSpec, preds: dict[str, float]) -> Optional[bool]:
        if spec.applies_when and not self._positive(self._spec(spec.applies_when), preds):
            return None
        if spec.task == "multiclass":
            siblings = [s.id for s in self.registry() if s.probe == spec.probe]
            return bool(max(siblings, key=lambda i: preds[i]) == spec.id)
        return bool(preds[spec.id] >= spec.threshold)

    def _spec(self, head_id: str) -> HeadSpec:
        return next(s for s in self.registry() if s.id == head_id)

    def _head_entry(self, spec: HeadSpec, preds: dict[str, float]) -> dict:
        applicable = spec.applies_when is None or bool(self._positive(self._spec(spec.applies_when), preds))
        return {
            "id": spec.id,
            "label": spec.label,
            "description": spec.description,
            "task": spec.task,
            "kind": spec.kind,
            "group": spec.group,
            "probability": preds[spec.id],
            "threshold": spec.threshold,
            "positive": self._positive(spec, preds),
            "applicable": applicable,
            "applicableWhen": spec.applies_when,
        }

    def _format_predictions(self, preds: dict[str, float], source: str, heads: list[HeadSpec]) -> dict:
        return {
            "heads": [self._head_entry(spec, preds) for spec in heads],
            "input": {"source": source, "description": SOURCE_LABELS[source]["en"]},
            "notes": THRESHOLD_NOTE,
        }

    def _diagnosis_text(self, preds: dict[str, float], source: str, heads: list[HeadSpec]) -> str:
        parts = []
        ids = {s.id for s in heads}
        if PRIMARY_HEAD in ids:
            aco = preds[PRIMARY_HEAD]
            positive = self._positive(self._spec(PRIMARY_HEAD), preds)
            text = (f"{'occlusion suspected' if positive else 'occlusion not suspected'} (ACO probability "
                    f"{aco:.3f}, threshold {self._spec(PRIMARY_HEAD).threshold:.4f})")
            if positive and SECONDARY_HEAD in ids:
                kind = "complete (ACCO)" if self._positive(self._spec(SECONDARY_HEAD), preds) else "incomplete (AICO)"
                text += f"; {kind} more likely (ACCO probability {preds[SECONDARY_HEAD]:.3f})"
            parts.append(text)
        others = [s for s in heads if s.id not in (PRIMARY_HEAD, SECONDARY_HEAD)]
        flagged = [s for s in others if self._positive(s, preds)]
        if others:
            parts.append("other positive outputs: " + (", ".join(
                f"{s.label} ({preds[s.id]:.3f})" for s in flagged) if flagged else "none")
                + f" of {len(others)}")
        return f"DeepECG-CLIP-DINOv3: {'; '.join(parts)}; input: {source} | research use only, not a clinical diagnosis"

    def _recommendations(self, preds: dict[str, float], source: str, heads: list[HeadSpec]) -> dict[str, object]:
        ids = {s.id for s in heads}
        if PRIMARY_HEAD in ids:
            aco = preds[PRIMARY_HEAD]
            if self._positive(self._spec(PRIMARY_HEAD), preds):
                kind_en = kind_fr = ""
                if SECONDARY_HEAD in ids:
                    acco = preds[SECONDARY_HEAD]
                    if self._positive(self._spec(SECONDARY_HEAD), preds):
                        kind_en = f"; complete occlusion (ACCO) more likely (probability {acco:.3f})"
                        kind_fr = f" ; occlusion complète (ACCO) plus probable (probabilité {acco:.3f})"
                    else:
                        kind_en = f"; incomplete occlusion (AICO) more likely (probability {acco:.3f})"
                        kind_fr = f" ; occlusion incomplète (AICO) plus probable (probabilité {acco:.3f})"
                en = (f"<strong>Acute coronary occlusion suspected (probability {aco:.3f}).</strong> "
                      f"The model output is above the research threshold{kind_en}. Correlate with symptoms, "
                      "serial ECGs and troponin. Research use only: do not change management on the basis "
                      "of this output.")
                fr = (f"<strong>Occlusion coronarienne aiguë suspectée (probabilité {aco:.3f}).</strong> "
                      f"La sortie du modèle dépasse le seuil de recherche{kind_fr}. Corréler avec les symptômes, "
                      "les ECG sériés et la troponine. Usage en recherche seulement : ne pas modifier la prise "
                      "en charge sur la base de ce résultat.")
            else:
                en = (f"<strong>Acute coronary occlusion not suspected (probability {aco:.3f}).</strong> "
                      "The model output is below the research threshold. This does not exclude an acute "
                      "coronary syndrome or an occlusion; interpret the ECG clinically. Research use only.")
                fr = (f"<strong>Occlusion coronarienne aiguë non suspectée (probabilité {aco:.3f}).</strong> "
                      "La sortie du modèle est sous le seuil de recherche. Cela n'exclut pas un syndrome "
                      "coronarien aigu ni une occlusion ; interpréter l'ECG cliniquement. Usage en recherche "
                      "seulement.")
        else:
            en = ("Research outputs of the DeepECG-CLIP DINOv3 ECG model; see each output's probability "
                  "and threshold. Research use only.")
            fr = ("Sorties de recherche du modèle ECG DeepECG-CLIP DINOv3 ; voir la probabilité et le seuil "
                  "de chaque sortie. Usage en recherche seulement.")
        if source == "image":
            en += " The ECG was read from an image, not the original signal; reliability is lower."
            fr += " L'ECG a été lu à partir d'une image et non du signal original ; la fiabilité est moindre."
        return {"en": en, "fr": fr, "presentable": True}

    def _json_result(self, preds: dict[str, float], source: str, heads: list[HeadSpec]) -> dict:
        return {
            "diagnosis": self._diagnosis_text(preds, source, heads),
            "predictions": self._format_predictions(preds, source, heads),
            "modelRecommendations": self._recommendations(preds, source, heads),
        }

    # -- HTML report -------------------------------------------------------------

    @staticmethod
    def _escape(text: str) -> str:
        return (str(text).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
                .replace('"', "&quot;"))

    def _render_html(self, preds: dict[str, float], source: str, heads: list[HeadSpec]) -> str:
        recs = self._recommendations(preds, source, heads)
        rows = []
        for spec in heads:
            positive = self._positive(spec, preds)
            if positive is None:
                result = "Not applicable / Sans objet"
            elif spec.task == "multiclass":
                result = "Most probable / Plus probable" if positive else "&mdash;"
            else:
                result = "Positive / Positif" if positive else "Negative / Négatif"
            css = "flag-pos" if positive else "flag-neg"
            threshold = "&mdash;" if spec.threshold is None else f"{spec.threshold:.4f}"
            rows.append(f'<tr><td>{self._escape(spec.label)}</td><td>{preds[spec.id]:.3f}</td>'
                        f'<td>{threshold}</td><td class="{css}">{result}</td></tr>')
        headline = ""
        if PRIMARY_HEAD in {s.id for s in heads}:
            positive = self._positive(self._spec(PRIMARY_HEAD), preds)
            status = ("Occlusion suspected / Occlusion suspectée" if positive
                      else "Occlusion not suspected / Occlusion non suspectée")
            headline = (f'<div class="score"><strong>{preds[PRIMARY_HEAD]:.3f}</strong>'
                        '<span lang="en">ACO probability (model output)</span>'
                        '<span class="translation" lang="fr">Probabilité d\'OCA (sortie du modèle)</span>'
                        f'<div class="{"flag-pos" if positive else "flag-neg"}">{status}</div></div>')
        source_en, source_fr = SOURCE_LABELS[source]["en"], SOURCE_LABELS[source]["fr"]
        table = "\n ".join(rows)
        return f"""<!DOCTYPE html>
<html><head><meta charset=\"utf-8\"><title>DeepECG-CLIP-DINOv3 Report</title>
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
 .warn p{{margin:8px 0}} .source{{color:#526171;font-size:.94em;margin-top:12px}}
</style></head><body><div class=\"card\">
 <h1>DeepECG-CLIP-DINOv3</h1>
 <div class=\"subtitle\"><span lang=\"en\">Research outputs from the 12-lead ECG</span>
 <span class=\"translation\" lang=\"fr\">Sorties de recherche dérivées de l'ECG 12 dérivations</span></div>
 {headline}
 <table><tr><th>Output / Sortie</th><th>Probability / Probabilité</th><th>Threshold / Seuil</th><th>Result / Résultat</th></tr>
 {table}</table>
 <div class=\"source\"><span lang=\"en\">Input: {source_en}</span><span class=\"translation\" lang=\"fr\">Entrée : {source_fr}</span></div>
 <div class=\"rec\"><h2>Research recommendation / Recommandation de recherche</h2>
 <p lang=\"en\">{recs["en"]}</p><p lang=\"fr\">{recs["fr"]}</p></div>
 <div class=\"warn\"><p lang=\"en\"><strong>Research use only — not for clinical decision-making.</strong>
 The probabilities are model outputs, not calibrated risks; thresholds are validation Youden points. On the
 held-out Montreal Heart Institute ACS test set (9,021 ECGs, 589 occlusions) the ACO output reached an AUROC of
 0.92 with a sensitivity of 0.83 and a specificity of 0.87 at its threshold; the ACCO vs AICO output reached an
 AUROC of 0.69 among occlusions only. No external validation and no calibration analysis yet. No regulatory
 clearance.</p>
 <p lang=\"fr\"><strong>Usage en recherche seulement — non destiné à la prise de décision clinique.</strong>
 Les probabilités sont des sorties du modèle et non des risques étalonnés ; les seuils sont des points de Youden de
 validation. Sur l'ensemble de test SCA de l'Institut de Cardiologie de Montréal (9 021 ECG, 589 occlusions), la
 sortie OCA a obtenu une ASC de 0,92, une sensibilité de 0,83 et une spécificité de 0,87 à son seuil ; la sortie
 ACCO/AICO a obtenu une ASC de 0,69 parmi les occlusions seulement. Pas encore de validation externe ni d'analyse
 d'étalonnage. Aucune autorisation réglementaire.</p></div>
</div></body></html>"""

    # -- report of a stored result ---------------------------------------------

    @classmethod
    def report_html(cls, result) -> str:
        """The HTML report of a stored JSON-mode result, identical to what HTML-mode predict returns for the
        same prediction. No model, no weights, no GPU. Predict rounds every probability to DECIMALS before
        anything reads it, so the stored probabilities are the values the report was drawn from: the whole
        result is regenerated from them by the predict code and must equal the stored one, or ResultError is
        raised. Nothing stored is copied into the page."""
        result = unwrap(result)
        if not result["predictions"]:
            if difference(NO_ECG_RESULT, result) is not None:
                fail("predictions", "is empty but the result is not the no-ECG answer")
            return NO_ECG_HTML
        service = cls()
        heads, preds, source = service._stored_inputs(result["predictions"])
        regenerated = JsonPredictionResponse(**service._json_result(preds, source, heads)).model_dump()
        where = difference(regenerated, result)
        if where is not None:
            fail(where, "is not what this model writes for these predictions")
        return service._render_html(preds, source, heads)

    def _stored_inputs(self, predictions: dict) -> tuple[list[HeadSpec], dict[str, float], str]:
        source = member(member(predictions, "input", "predictions"), "source", "predictions.input")
        if source not in SOURCE_LABELS:
            fail("predictions.input.source", "is not an input this model reads")
        entries = member(predictions, "heads", "predictions")
        if not isinstance(entries, list) or not entries:
            fail("predictions.heads", "must be a non-empty list")
        by_id = {s.id: s for s in self.registry()}
        stored: dict[str, float] = {}
        for k, entry in enumerate(entries):
            head_id = member(entry, "id", f"predictions.heads[{k}]")
            if head_id not in by_id or head_id in stored:
                fail(f"predictions.heads[{k}].id", "is not a head of this model, or is repeated")
            stored[head_id] = number(member(entry, "probability", f"predictions.heads[{k}]"),
                                     f"predictions.heads[{k}].probability", 0.0, 1.0, DECIMALS)
        heads = [s for s in self.registry() if s.id in stored]
        if [s.id for s in heads] != list(stored):
            fail("predictions.heads", "are not in the order this model writes them")
        return heads, stored, source

    # -- handlers ------------------------------------------------------------------

    async def _handle_json_output(self, request: PredictRequest):
        heads = self._selected_heads(request)
        outputs = self._run_inference(self._decode_dicoms(request), heads)
        if outputs is None:
            return json.loads(json.dumps(NO_ECG_RESULT))
        raw, source = outputs
        return self._json_result(self._postprocess(raw), source, heads)

    async def _handle_html_output(self, request: PredictRequest):
        heads = self._selected_heads(request)
        outputs = self._run_inference(self._decode_dicoms(request), heads)
        if outputs is None:
            html = NO_ECG_HTML
        else:
            raw, source = outputs
            html = self._render_html(self._postprocess(raw), source, heads)
        return {"htmlBase64": base64.b64encode(html.encode("utf-8")).decode("utf-8")}
