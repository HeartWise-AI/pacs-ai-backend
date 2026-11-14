"""Display calibration of the per-segment stenosis estimate (display only; `stenosis_pct` is never changed).

The regression head is pulled toward the mean: true 0% segments are often estimated at 5-10% and true >= 70% lesions
at 35-50%. The report therefore shows a rescaled value, read from data/stenosis_display.json:

- an estimate below `zero_below_pct` is shown as 0% ("normal");
- above it, a monotone piecewise-linear map through the knots puts the values on the angiographic categories
  0, < 30 (mild), 30-49, 50-69 and >= 70;
- a CTO-flagged segment is shown as 100% (occluded);
- a segment flagged for significant stenosis is never shown as normal: it is shown at least at the lowest
  non-zero displayed value (the bottom of the mild category).

Pure Python (no numpy), so the report renders from a stored JSON result.
"""
from __future__ import annotations

import json
from functools import lru_cache
from pathlib import Path

CALIBRATION_PATH = Path(__file__).resolve().parent / "data" / "stenosis_display.json"
CATEGORY_LABELS = {"normal": "Normal", "mild": "Mild (<30%)", "moderate": "30-49%", "intermediate": "50-69%",
                   "severe": "≥70%", "occluded": "Occluded"}


@lru_cache(maxsize=1)
def calibration() -> dict:
    table = json.loads(CALIBRATION_PATH.read_text(encoding="utf-8"))
    xs, ys = table["knots"]["estimate_pct"], table["knots"]["display_pct"]
    if (len(xs) != len(ys) or len(xs) < 2 or xs[0] != table["zero_below_pct"] or xs[-1] != 100
            or any(b <= a for a, b in zip(xs, xs[1:])) or any(b < a for a, b in zip(ys, ys[1:]))
            or ys[0] <= 0 or ys[-1] != 100):
        raise ValueError(f"{CALIBRATION_PATH.name}: knots must start at zero_below_pct, end at 100 and increase")
    return table


def _interp(x: float, xs: list, ys: list) -> float:
    for (x0, y0), (x1, y1) in zip(zip(xs, ys), zip(xs[1:], ys[1:])):
        if x <= x1:
            return y0 + (y1 - y0) * (x - x0) / (x1 - x0)
    return ys[-1]


def display_pct(v: dict) -> int:
    """The % shown in the report for one segment of `predictions.segments` (an integer, 0 to 100)."""
    if v.get("cto"):
        return 100
    table = calibration()
    xs, ys = table["knots"]["estimate_pct"], table["knots"]["display_pct"]
    pct = min(max(float(v["stenosis_pct"]), 0.0), 100.0)
    if pct < table["zero_below_pct"]:
        return int(ys[0]) if v.get("significant") else 0
    return int(round(_interp(pct, xs, ys)))


def category(pct: float, cto: bool = False) -> str:
    """Angiographic category of a displayed %: normal, mild (< 30), moderate (30-49), intermediate (50-69),
    severe (>= 70) or occluded (CTO flag)."""
    if cto:
        return "occluded"
    return ("normal" if pct <= 0 else "mild" if pct < 30 else "moderate" if pct < 50
            else "intermediate" if pct < 70 else "severe")


def display_category(v: dict) -> str:
    return category(display_pct(v), bool(v.get("cto")))


def add_display(segments: dict) -> dict:
    """Adds `display_pct` and `display_category` to each segment of `predictions.segments` (in place)."""
    for v in segments.values():
        v["display_pct"] = display_pct(v)
        v["display_category"] = display_category(v)
    return segments


def version() -> str:
    return calibration()["version"]
