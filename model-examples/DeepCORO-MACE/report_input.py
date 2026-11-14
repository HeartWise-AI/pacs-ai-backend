"""Checks a stored JSON-mode result before /inference/render-report draws it again. Pure Python (no torch).

A stored result is the `data` object of a JSON-mode predict response ({diagnosis, predictions,
modelRecommendations}). The HTML report is not drawn from its strings. The model's logic.py reads the stored
numbers with the checks below (type, finite, range, decimals), proposes raw model outputs that round to them
(`nudged`), regenerates the JSON output of each with the code that serves predict, and renders the report from
the first one whose regenerated output is the stored result (`difference` is None). A value or string the model
would not have written for these numbers (for example recommendation HTML that was edited) therefore never
reaches the page.

More than one raw output is tried because the JSON rounds what the report reads unrounded: a value stored as
0.500 may have been just below a 0.5 threshold, and a report line may print fewer decimals than the JSON keeps.
"""
from __future__ import annotations

import math

NO_VIDEO_HTML = "<h1>No video could be extracted from the current DICOM series</h1>"
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


def nudged(value: float, decimals: int, lo: float, hi: float) -> list[float]:
    """Raw values that round to the stored `value`: the value itself, then values on each side of it inside its
    rounding interval, so that a threshold or a coarser rounding that falls inside the interval is met from
    both sides."""
    step = 10.0 ** -decimals
    out = [value]
    for offset in (-0.4, 0.4, -0.499, 0.499):
        candidate = value + offset * step
        if lo <= candidate <= hi and round(candidate, decimals) == value and candidate not in out:
            out.append(candidate)
    return out


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
