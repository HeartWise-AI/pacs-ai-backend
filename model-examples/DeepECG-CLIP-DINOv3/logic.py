import base64
import json
import os
from dataclasses import dataclass
from io import BytesIO
from typing import Optional

import pydicom
import torch
from models.aco_finetune import FineTunedACO
from models.dinov3_bank import ALL_77, GATED_HEADS, PROBE_OUTPUTS, DeepECG_DINOv3_Predictor
from report_input import NO_ECG_HTML, NO_ECG_RESULT, difference, fail, member, number, unwrap
from utils.ecg_inputs import EcgInputError, EcgPage, ecg_page, order_candidates
from utils.genericLogic import BasePredictionService
from utils.http_utils import Config, JsonPredictionResponse, PredictRequest
from utils.image_preprocessing import preprocess_ecg_image

MODEL_DIRECTORY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "models")
PRIMARY_HEAD = "aco"
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
    "Research use only: probabilities are model outputs (aco: the fully fine-tuned DINOv3 model; the others: "
    "frozen linear probes on the DINOv3-SigLIP2 ECG encoder), not calibrated risks, with no external validation. "
    "A flag (positive) is given only where a validation Youden threshold exists; the 77 ECG findings are "
    "probabilities only."
)


@dataclass(frozen=True)
class HeadSpec:
    """One reported output (DeepECG_DINOv3_Predictor row): a single-output probe task, or one output of a
    multi-output task (id "<task>:<output>")."""
    id: str
    head: str                  # probe task (ecg77, shd, ...)
    label: str
    threshold: Optional[float]
    applies_when: Optional[str] = None


def _read_json(path: str) -> dict:
    with open(path) as fp:
        return json.load(fp)


def model_config() -> dict:
    cfg = _read_json(os.path.join(MODEL_DIRECTORY, "config.json"))
    heads, disabled, fine = cfg["heads"], set(cfg["disabled_heads"]), set(cfg.get("fine_tuned", {}))
    if cfg["variant"] not in ("clean", "aug") or not heads or set(heads) & disabled \
            or not set(heads) <= set(PROBE_OUTPUTS) or fine & (set(heads) | disabled - {"aco"}) \
            or fine - {"aco"}:
        raise ValueError("models/config.json: invalid variant or head selection")
    return cfg


def head_registry(cfg: dict) -> list[HeadSpec]:
    """Every output served, read from the enabled heads' head.json and thresholds.json (no weights):
    the fine-tuned ACO first, binary heads in configuration order, then the multi-output heads (the 77 ECG
    findings)."""
    specs = [HeadSpec(head, head, entry["label"], float(entry["threshold"]))
             for head, entry in cfg.get("fine_tuned", {}).items()]
    ordered = sorted(cfg["heads"], key=lambda task: PROBE_OUTPUTS[task] == ALL_77 or len(PROBE_OUTPUTS[task]) > 1)
    for task in ordered:
        folder = os.path.join(MODEL_DIRECTORY, "probes", f"{task}_{cfg['variant']}")
        meta = _read_json(os.path.join(folder, "head.json"))
        thresholds = _read_json(os.path.join(folder, "thresholds.json"))["thresholds"]
        outputs = meta["outputs"]
        for output in outputs:
            single = len(outputs) == 1
            specs.append(HeadSpec(task if single else f"{task}:{output}", task,
                                  str(meta.get("task") or task) if single else output,
                                  None if output not in thresholds else float(thresholds[output]),
                                  GATED_HEADS.get(task)))
    return specs


class CustomPredictionService(BasePredictionService):
    PRIMARY_HEAD = PRIMARY_HEAD

    def load_model(self, config: Config):
        if CustomPredictionService.is_initialized:
            return
        cfg = model_config()
        device = "cuda:0" if torch.cuda.is_available() else "cpu"
        # The encoder, the head checks and the probabilities are DeepECG_Docker's (models/dinov3_bank.py).
        predictor = DeepECG_DINOv3_Predictor(cfg["variant"], device, snapshot_folder=MODEL_DIRECTORY,
                                             heads=tuple(cfg["heads"]))
        CustomPredictionService.models["predictor"] = predictor
        # The fully fine-tuned ACO model: its own backbone, fp32, on the same preprocessed page.
        for head, entry in cfg.get("fine_tuned", {}).items():
            model = FineTunedACO()
            model.load_checkpoint(os.path.join(MODEL_DIRECTORY, entry["path"]))
            CustomPredictionService.models[f"fine_tuned:{head}"] = model.eval().to(device)
        CustomPredictionService._registry = head_registry(cfg)
        CustomPredictionService.is_initialized = True
        print(f"DeepECG-CLIP-DINOv3 loaded: {cfg['variant']} probes {', '.join(cfg['heads'])}; "
              f"{len(CustomPredictionService._registry)} outputs. CUDA: {torch.cuda.is_available()}")

    def stop_model(self):
        for key in [k for k in CustomPredictionService.models if k == "predictor" or k.startswith("fine_tuned:")]:
            CustomPredictionService.models.pop(key, None)
        CustomPredictionService.is_initialized = False
        if torch.cuda.is_available():
            torch.cuda.empty_cache()

    @classmethod
    def registry(cls) -> list[HeadSpec]:
        """The outputs; without a loaded model (render-report) they are read from the head files."""
        if not hasattr(cls, "_registry"):
            cls._registry = head_registry(model_config())
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
        """All outputs, or those named in additionalMetadata.heads (output ids or probe tasks: "ecg77"
        selects its 77 outputs). Disabled heads (models/config.json) are refused."""
        registry = self.registry()
        wanted = (request.additionalMetadata or {}).get("heads")
        if wanted is None:
            return list(registry)
        if not isinstance(wanted, list) or not all(isinstance(w, str) for w in wanted):
            raise ValueError("additionalMetadata.heads must be a list of output ids or head names")
        known = {s.id for s in registry} | {s.head for s in registry}
        unknown = sorted(set(wanted) - known)
        if unknown:
            raise ValueError(f"Unknown or disabled head(s): {', '.join(unknown)}")
        chosen = {s.id for s in registry if s.id in wanted or s.head in wanted}
        chosen |= {s.applies_when for s in registry if s.id in chosen and s.applies_when}
        return [s for s in registry if s.id in chosen]

    def _run_inference(self, dicoms: list[pydicom.Dataset]) -> Optional[tuple[dict[str, float], str]]:
        """{output id: probability rounded to DECIMALS} of every enabled head (one encoder pass), and the
        input kind. Flags, text and the report read the rounded values the JSON stores, so a stored result
        holds everything needed to draw its report again."""
        page = self._select_page(dicoms)
        if page is None:
            return None
        predictor = CustomPredictionService.models["predictor"]
        tensor = preprocess_ecg_image(page.image, predictor.bank.preprocessing)
        rows = {row["id"]: float(row["probability"]) for row in predictor.predict_tensor(tensor)["heads"]}
        for key, model in CustomPredictionService.models.items():
            if key.startswith("fine_tuned:"):
                rows[key.split(":", 1)[1]] = float(model.probability(tensor.unsqueeze(0))[0])
        return {key: round(value, DECIMALS) for key, value in rows.items()}, page.source

    def _positive(self, spec: HeadSpec, preds: dict[str, float]) -> Optional[bool]:
        if spec.applies_when and not self._positive(self._spec(spec.applies_when), preds):
            return None
        if spec.threshold is None:
            return None   # a probability-only output (DeepECG_Docker thresholds.json without cutoffs)
        return bool(preds[spec.id] >= spec.threshold)

    def _spec(self, head_id: str) -> HeadSpec:
        return next(s for s in self.registry() if s.id == head_id)

    def _head_entry(self, spec: HeadSpec, preds: dict[str, float]) -> dict:
        applicable = spec.applies_when is None or bool(self._positive(self._spec(spec.applies_when), preds))
        # The fields of DeepECG_Docker's DeepECG_DINOv3_Predictor rows
        return {
            "id": spec.id,
            "label": spec.label,
            "probability": preds[spec.id],
            "threshold": spec.threshold,
            "positive": self._positive(spec, preds),
            "applicable": applicable,
        }

    def _format_predictions(self, preds: dict[str, float], source: str, heads: list[HeadSpec]) -> dict:
        return {
            "heads": [self._head_entry(spec, preds) for spec in heads],
            "input": {"source": source, "description": SOURCE_LABELS[source]["en"]},
            "model": {"encoder": "DINOv3-SigLIP2 zo59vdxk epoch 30", "probes": model_config()["variant"],
                      "fine_tuned": sorted(model_config().get("fine_tuned", {}))},
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
            parts.append(text)
        others = [s for s in heads if s.id != PRIMARY_HEAD and s.threshold is not None]
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
        rows, findings = [], []
        for spec in sorted((h for h in heads if h.threshold is None and ":" in h.id), key=lambda h: -preds[h.id]):
            findings.append(f'<tr><td>{self._escape(spec.label)}</td><td>{preds[spec.id]:.3f}</td></tr>')
        for spec in (h for h in heads if not (h.threshold is None and ":" in h.id)):
            positive = self._positive(spec, preds)
            if positive is None and spec.threshold is None:
                result = "Probability only / Probabilité seulement"
            elif positive is None:
                result = "Not applicable / Sans objet"
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
        findings_html = ""
        if findings:
            findings_html = ('<h2>ECG findings, probabilities only (most probable first) / Constats ECG, '
                             'probabilités seulement</h2><table><tr><th>Finding / Constat</th>'
                             '<th>Probability / Probabilité</th></tr>\n ' + "\n ".join(findings) + '</table>')
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
 <div class=\"subtitle\"><span lang=\"en\">Research outputs from the 12-lead ECG (DINOv3-SigLIP2 encoder, frozen probes)</span>
 <span class=\"translation\" lang=\"fr\">Sorties de recherche dérivées de l'ECG 12 dérivations</span></div>
 {headline}
 <table><tr><th>Output / Sortie</th><th>Probability / Probabilité</th><th>Threshold / Seuil</th><th>Result / Résultat</th></tr>
 {table}</table>
 {findings_html}
 <div class=\"source\"><span lang=\"en\">Input: {source_en}</span><span class=\"translation\" lang=\"fr\">Entrée : {source_fr}</span></div>
 <div class=\"rec\"><h2>Research recommendation / Recommandation de recherche</h2>
 <p lang=\"en\">{recs["en"]}</p><p lang=\"fr\">{recs["fr"]}</p></div>
 <div class=\"warn\"><p lang=\"en\"><strong>Research use only — not for clinical decision-making.</strong>
 The probabilities are outputs of the fully fine-tuned DINOv3 ACO model and of frozen linear probes on the
 DINOv3-SigLIP2 ECG image encoder, not calibrated risks; flags use validation Youden thresholds, and the 77 ECG findings have none. Held-out Montreal Heart
 Institute test AUROCs (clean pages): ACO 0.92 (fully fine-tuned model; sensitivity 0.83, specificity 0.87 at its
 threshold), LVEF &le; 40%
 0.89, LVEF &lt; 50% 0.87, SHD 0.77, HCM 0.71, incident AFib at 5 years 0.68, 77 findings 0.97 (macro). No external
 validation and no calibration analysis. No regulatory clearance.</p>
 <p lang=\"fr\"><strong>Usage en recherche seulement — non destiné à la prise de décision clinique.</strong>
 Les probabilités sont des sorties de sondes linéaires figées sur l'encodeur d'images ECG DINOv3-SigLIP2, et non
 des risques étalonnés ; les indicateurs utilisent des seuils de Youden de validation, et les 77 constats ECG n'en
 ont pas. ASC sur l'ensemble de test de l'Institut de Cardiologie de Montréal (pages propres) : OCA 0,92 (modèle entièrement affiné ; sensibilité 0,83, spécificité 0,87 à son seuil), FEVG &le; 40 % 0,89, FEVG &lt; 50 % 0,87, cardiopathie structurelle
 0,77, CMH 0,71, FA incidente à 5 ans 0,68, 77 constats 0,97 (macro). Aucune validation externe ni analyse
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
        outputs = self._run_inference(self._decode_dicoms(request))
        if outputs is None:
            return json.loads(json.dumps(NO_ECG_RESULT))
        raw, source = outputs
        return self._json_result(raw, source, heads)

    async def _handle_html_output(self, request: PredictRequest):
        heads = self._selected_heads(request)
        outputs = self._run_inference(self._decode_dicoms(request))
        if outputs is None:
            html = NO_ECG_HTML
        else:
            raw, source = outputs
            html = self._render_html(raw, source, heads)
        return {"htmlBase64": base64.b64encode(html.encode("utf-8")).decode("utf-8")}
