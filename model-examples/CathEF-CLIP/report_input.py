"""Checks a stored JSON-mode result before /inference/render-report draws it again. Pure Python (no torch).

A stored result is the `data` object of a JSON-mode predict response:

    {"diagnosis": "...", "predictions": {"LVEF": {"value", "unit"}, "reducedEF": {"probability", "threshold",
     "diagnosis"}}, "modelRecommendations": {"en", "fr", "presentable"}}

The HTML report is not drawn from those strings. `checked_result` checks the type and range of every value, and
`representatives` proposes raw model outputs (LVEF, P(EF<40%)) that could have produced the stored numbers. The
caller (logic.CustomPredictionService.report_html) keeps the first one whose regenerated JSON output equals the
stored result exactly, and renders the report from it. A string that the model code would not have written for
these numbers (for example recommendation HTML that was edited) therefore never reaches the page.

More than one representative is needed because the JSON rounds what the report reads unrounded: the recommendation
branch uses the raw LVEF (39.96 is stored as 40.0 but is "< 40"), the report prints the probability with two
decimals (stored with three) and compares the raw probability with the threshold.
"""
from __future__ import annotations

import math

NO_VIDEO_RESULT = {
    "diagnosis": "No video could be extracted from the current DICOM series",
    "predictions": {},
    "modelRecommendations": {
        "en": "No video could be extracted or processed.",
        "fr": "Aucune vidéo n'a pu être extraite ou traitée.",
        "presentable": False,
    },
}
RESULT_KEYS = ("diagnosis", "predictions", "modelRecommendations")
MAX_DIAGNOSIS_CHARS = 200
MAX_RECOMMENDATION_CHARS = 1000
# Half of the last stored decimal, slightly reduced so that rounding returns the stored value.
LVEF_NUDGE = 0.04
PROBABILITY_NUDGE = 0.0004


class ResultError(ValueError):
    pass


def _fail(path: str, why: str):
    raise ResultError(f"{path}: {why}")


def _object(x, path: str, keys: tuple[str, ...]) -> dict:
    if not isinstance(x, dict):
        _fail(path, "must be an object")
    if set(x) != set(keys):
        _fail(path, f"must have exactly the keys {', '.join(keys)}")
    return x


def _number(x, path: str, lo: float, hi: float, decimals: int) -> float:
    if isinstance(x, bool) or not isinstance(x, (int, float)):
        _fail(path, "must be a finite number")
    try:
        value = float(x)
    except OverflowError:
        _fail(path, "must be a finite number")
    if not math.isfinite(value):
        _fail(path, "must be a finite number")
    if not lo <= value <= hi:
        _fail(path, f"must be within [{lo}, {hi}]")
    if round(value, decimals) != value:
        _fail(path, f"must have at most {decimals} decimal(s)")
    return value


def _text(x, path: str, limit: int) -> str:
    if not isinstance(x, str) or len(x) > limit:
        _fail(path, f"must be a string of at most {limit} characters")
    return x


def checked_result(result, threshold: float) -> dict | None:
    """The stored result with its numbers as floats, or None for the stored no-video answer.
    Accepts the `data` object or the whole response envelope ({"success", "data": {...}})."""
    if not isinstance(result, dict):
        _fail("result", "must be an object")
    if "predictions" not in result and isinstance(result.get("data"), dict):
        result = result["data"]
    _object(result, "result", RESULT_KEYS)
    diagnosis = _text(result["diagnosis"], "diagnosis", MAX_DIAGNOSIS_CHARS)

    recs = _object(result["modelRecommendations"], "modelRecommendations", ("en", "fr", "presentable"))
    en = _text(recs["en"], "modelRecommendations.en", MAX_RECOMMENDATION_CHARS)
    fr = _text(recs["fr"], "modelRecommendations.fr", MAX_RECOMMENDATION_CHARS)
    if not isinstance(recs["presentable"], bool):
        _fail("modelRecommendations.presentable", "must be true or false")

    preds = result["predictions"]
    if not isinstance(preds, dict):
        _fail("predictions", "must be an object")
    if not preds:
        if result != NO_VIDEO_RESULT:
            _fail("predictions", "is empty but the result is not the no-video answer")
        return None

    _object(preds, "predictions", ("LVEF", "reducedEF"))
    lvef = _object(preds["LVEF"], "predictions.LVEF", ("value", "unit"))
    if lvef["unit"] != "%":
        _fail("predictions.LVEF.unit", 'must be "%"')
    reduced = _object(preds["reducedEF"], "predictions.reducedEF", ("probability", "threshold", "diagnosis"))
    if _number(reduced["threshold"], "predictions.reducedEF.threshold", 0.0, 1.0, 6) != threshold:
        _fail("predictions.reducedEF.threshold", f"must be the model threshold {threshold}")
    if not isinstance(reduced["diagnosis"], str) or reduced["diagnosis"] not in ("reduced", "preserved"):
        _fail("predictions.reducedEF.diagnosis", "must be reduced or preserved")
    if recs["presentable"] is not True:
        _fail("modelRecommendations.presentable", "must be true for a result with predictions")

    return {
        "diagnosis": diagnosis,
        "predictions": {
            "LVEF": {"value": _number(lvef["value"], "predictions.LVEF.value", 0.0, 100.0, 1), "unit": "%"},
            "reducedEF": {
                "probability": _number(reduced["probability"], "predictions.reducedEF.probability", 0.0, 1.0, 3),
                "threshold": threshold,
                "diagnosis": reduced["diagnosis"],
            },
        },
        "modelRecommendations": {"en": en, "fr": fr, "presentable": True},
    }


def representatives(checked: dict) -> list[dict[str, float]]:
    """Raw model outputs ({"Value": LVEF %, "y_true_cat": P(EF<40%)}) that round to the stored numbers: the stored
    value itself and one just below it for the LVEF, the stored value and one on each side for the probability."""
    lvef = checked["predictions"]["LVEF"]["value"]
    probability = checked["predictions"]["reducedEF"]["probability"]
    lvefs = [lvef] + ([lvef - LVEF_NUDGE] if lvef - LVEF_NUDGE >= 0.0 else [])
    probabilities = [p for p in (probability, probability - PROBABILITY_NUDGE, probability + PROBABILITY_NUDGE)
                     if 0.0 <= p <= 1.0]
    return [{"Value": v, "y_true_cat": p} for v in lvefs for p in probabilities]
