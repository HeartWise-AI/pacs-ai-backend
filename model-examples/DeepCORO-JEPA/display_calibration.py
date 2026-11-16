"""Display calibration of the per-segment stenosis estimate (display only; `stenosis_pct` is never changed).

The regression head is pulled toward the mean: true 0% segments are often estimated at 5-10% and true >= 70% lesions
at 35-50%. The report therefore shows a rescaled value. The mapping belongs to one model version:

- data/stenosis_display.json is the mapping of the served model (v15);
- data/stenosis_display_v13.json is the mapping of model v13, kept so that /inference/render-report can draw a
  result stored by containers 1.1.0 to 1.4.1. A mapping is never applied to another model version.

For one mapping:

- an estimate below `zero_below_pct` is shown as 0% ("normal");
- above it, a monotone piecewise-linear map through the knots puts the values on the angiographic categories
  0, < 30 (mild), 30-49, 50-69 and >= 70. `rounding` says how the interpolated value becomes an integer: "nearest"
  (v13) or "down" (v15: an estimate below a knot stays in the category below it, as in the fit);
- a CTO-flagged segment is shown as 100% (occluded);
- a segment flagged for significant stenosis is never shown as normal: it is shown at least at the lowest
  non-zero displayed value (the bottom of the mild category).

A stored result keeps the `display_pct` and `display_category` it was returned with (1.4.0 on): `with_display` uses
them and computes the values only for results stored without them (1.2.0, 1.3.0), with the mapping of the model
version of that result. Results stored before 2.0.0 carry no `model_version`; they come from model v13.

Pure Python (no numpy), so the report renders from a stored JSON result.
"""
from __future__ import annotations

import json
import math
from functools import lru_cache
from pathlib import Path

DATA_DIR = Path(__file__).resolve().parent / "data"
CALIBRATION_PATH = DATA_DIR / "stenosis_display.json"  # the served model
STORED_RESULT_PATHS = (DATA_DIR / "stenosis_display_v13.json",)  # earlier models, for stored results only
LEGACY_MODEL_VERSION = "v13"  # a result without `model_version` was stored by 1.1.0 to 1.4.1, which served v13
ROUNDINGS = ("nearest", "down")
CATEGORY_LABELS = {"normal": "Normal", "mild": "Mild (<30%)", "moderate": "30-49%", "intermediate": "50-69%",
                   "severe": "≥70%", "occluded": "Occluded"}


def _load(path: Path) -> dict:
    table = json.loads(Path(path).read_text(encoding="utf-8"))
    xs, ys = table["knots"]["estimate_pct"], table["knots"]["display_pct"]
    if (len(xs) != len(ys) or len(xs) < 2 or xs[0] != table["zero_below_pct"] or xs[-1] != 100
            or any(b <= a for a, b in zip(xs, xs[1:])) or any(b < a for a, b in zip(ys, ys[1:]))
            or ys[0] <= 0 or ys[-1] != 100):
        raise ValueError(f"{Path(path).name}: knots must start at zero_below_pct, end at 100 and increase")
    if not isinstance(table.get("model_version"), str) or not table["model_version"]:
        raise ValueError(f"{Path(path).name}: model_version is required (a mapping belongs to one model version)")
    if table.get("rounding", "nearest") not in ROUNDINGS:
        raise ValueError(f"{Path(path).name}: rounding must be one of {ROUNDINGS}")
    return table


@lru_cache(maxsize=1)
def calibrations() -> dict:
    """Every mapping this container knows, by model version. The served one comes first."""
    tables = {}
    for path in (CALIBRATION_PATH, *STORED_RESULT_PATHS):
        table = _load(path)
        if table["model_version"] in tables:
            raise ValueError(f"{Path(path).name}: a second mapping for model {table['model_version']}")
        tables[table["model_version"]] = table
    return tables


def served_model_version() -> str:
    return next(iter(calibrations()))


def calibration(model_version: str | None = None) -> dict:
    """The mapping of `model_version` (None: the served model). Fails for a model version without a mapping: the
    mapping of another model is never used instead."""
    tables = calibrations()
    if model_version is None:
        return tables[served_model_version()]
    if model_version not in tables:
        raise ValueError(f"No display calibration for model version {model_version!r} (known: {sorted(tables)})")
    return tables[model_version]


def result_model_version(preds: dict) -> str:
    """Model version of a `predictions` object: its `model_version` (2.0.0 on), else v13."""
    return str(preds.get("model_version") or LEGACY_MODEL_VERSION)


def label(model_version: str | None = None) -> str:
    """What the report header prints: the model version and its display calibration ("v15-cal1")."""
    table = calibration(model_version)
    return f"{table['model_version']}-{table['version']}"


def _interp(x: float, xs: list, ys: list) -> float:
    for (x0, y0), (x1, y1) in zip(zip(xs, ys), zip(xs[1:], ys[1:])):
        if x <= x1:
            return y0 + (y1 - y0) * (x - x0) / (x1 - x0)
    return ys[-1]


def display_pct(v: dict, model_version: str | None = None) -> int:
    """The % shown in the report for one segment of `predictions.segments` (an integer, 0 to 100), computed from
    `stenosis_pct` and the flags with the mapping of `model_version`."""
    if v.get("cto"):
        return 100
    table = calibration(model_version)
    xs, ys = table["knots"]["estimate_pct"], table["knots"]["display_pct"]
    pct = min(max(float(v["stenosis_pct"]), 0.0), 100.0)
    if pct < table["zero_below_pct"]:
        return int(ys[0]) if v.get("significant") else 0
    value = _interp(pct, xs, ys)
    return int(math.floor(value + 1e-9)) if table.get("rounding", "nearest") == "down" else int(round(value))


def category(pct: float, cto: bool = False) -> str:
    """Angiographic category of a displayed %: normal, mild (< 30), moderate (30-49), intermediate (50-69),
    severe (>= 70) or occluded (CTO flag)."""
    if cto:
        return "occluded"
    return ("normal" if pct <= 0 else "mild" if pct < 30 else "moderate" if pct < 50
            else "intermediate" if pct < 70 else "severe")


def display_category(v: dict, model_version: str | None = None) -> str:
    return category(display_pct(v, model_version), bool(v.get("cto")))


def add_display(segments: dict, model_version: str | None = None, skip=()) -> dict:
    """Adds `display_pct` and `display_category` to each segment of `predictions.segments` (in place), except the
    segments in `skip` (unsupported outputs: nothing to display)."""
    for g, v in segments.items():
        if g in skip:
            continue
        v["display_pct"] = display_pct(v, model_version)
        v["display_category"] = display_category(v, model_version)
    return segments


def with_display(v: dict, model_version: str | None = None) -> dict:
    """The segment with its display fields: the stored `display_pct` / `display_category` when the result has
    them, else computed with the mapping of the result's model version. Never recomputes a stored value."""
    if "display_pct" in v and "display_category" in v:
        return v
    pct = int(v["display_pct"]) if "display_pct" in v else display_pct(v, model_version)
    return {**v, "display_pct": pct, "display_category": category(pct, bool(v.get("cto")))}


def version(model_version: str | None = None) -> str:
    return calibration(model_version)["version"]
