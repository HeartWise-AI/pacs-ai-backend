"""Checks a stored JSON-mode result before /inference/render-report draws it again. Pure Python (no torch).

A stored result is the `data` object of a JSON-mode predict response ({diagnosis, predictions,
modelRecommendations}). The HTML report is not drawn from its strings. The model's logic.py reads the stored
numbers with the checks below (type, finite, range, decimals), regenerates the whole JSON output from them with
the code that serves predict, and renders the report only when the regenerated output is the stored result
(`difference` is None). A value or string the model would not have written for these numbers (for example
recommendation HTML that was edited) therefore never reaches the page.
"""
from __future__ import annotations

import math

NO_ECG_HTML = "<h1>No readable ECG was found in the current DICOM study</h1>"
NO_ECG_RESULT = {
    "diagnosis": "No readable ECG was found in the current DICOM study",
    "predictions": {},
    "modelRecommendations": {
        "en": "No ECG waveform, vector ECG PDF or ECG image could be read.",
        "fr": "Aucun tracé ECG, PDF d'ECG vectoriel ni image d'ECG n'a pu être lu.",
        "presentable": False,
    },
}
RESULT_KEYS = ("diagnosis", "predictions", "modelRecommendations")


class ResultError(ValueError):
    pass


def fail(path: str, why: str):
    raise ResultError(f"{path}: {why}")


def member(x, key: str, path: str):
    """x[key] of an object, with the path of the member in the error."""
    if not isinstance(x, dict):
        fail(path, "must be an object")
    if key not in x:
        fail(f"{path}.{key}", "is missing")
    return x[key]


def number(x, path: str, lo: float, hi: float, decimals: int) -> float:
    """A stored number: finite, not a boolean, within [lo, hi], with at most `decimals` decimals."""
    if isinstance(x, bool) or not isinstance(x, (int, float)):
        fail(path, "must be a finite number")
    try:
        value = float(x)
    except OverflowError:
        fail(path, "must be a finite number")
    if not math.isfinite(value):
        fail(path, "must be a finite number")
    if not lo <= value <= hi:
        fail(path, f"must be within [{lo}, {hi}]")
    if round(value, decimals) != value:
        fail(path, f"must have at most {decimals} decimal(s)")
    return value


def boolean(x, path: str) -> bool:
    if not isinstance(x, bool):
        fail(path, "must be true or false")
    return x


def unwrap(result) -> dict:
    """The stored `data` object. The whole response envelope ({"success", "data": {...}}) is accepted too."""
    if not isinstance(result, dict):
        fail("result", "must be an object")
    if "predictions" not in result and isinstance(result.get("data"), dict):
        result = result["data"]
    if set(result) != set(RESULT_KEYS):
        fail("result", f"must have exactly the keys {', '.join(RESULT_KEYS)}")
    if not isinstance(result["predictions"], dict):
        fail("predictions", "must be an object")
    return result


def difference(expected, stored, path: str = "") -> str | None:
    """Path of the first place where `stored` is not `expected`, or None. Types are compared strictly (true is
    not 1, "1" is not 1); a whole number may be stored with or without a decimal point."""
    if isinstance(expected, dict):
        if not isinstance(stored, dict):
            return path or "result"
        for key in expected:
            if key not in stored:
                return f"{path}.{key}" if path else key
        for key in stored:
            if key not in expected:
                return path or "result"
        for key, value in expected.items():
            found = difference(value, stored[key], f"{path}.{key}" if path else key)
            if found is not None:
                return found
        return None
    if isinstance(expected, list):
        if not isinstance(stored, list) or len(stored) != len(expected):
            return path
        for index, value in enumerate(expected):
            found = difference(value, stored[index], f"{path}[{index}]")
            if found is not None:
                return found
        return None
    if expected is None or isinstance(expected, (bool, str)):
        return None if type(stored) is type(expected) and stored == expected else path
    if isinstance(expected, (int, float)):
        if isinstance(stored, bool) or not isinstance(stored, (int, float)):
            return path
        try:
            return None if math.isfinite(float(stored)) and float(stored) == float(expected) else path
        except OverflowError:
            return path
    return path
