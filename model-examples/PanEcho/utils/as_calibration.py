"""MHI recalibration of PanEcho's aortic stenosis outputs (PanEcho 1.1.3).

PanEcho's AVStenosis head was reported by argmax up to 1.1.1 and as raw screening scores in 1.1.2. On MHI data the study-level
probability of "Severe" stays low because most clips do not show the aortic valve, so argmax
returns "None" even for severe aortic stenosis. The AVPkVel head is shrunk toward the
training mean (about 1.7 m/s). This module applies the thresholds and the linear Vmax map
fitted on an MHI development set (see models/as_calibration.json and model_facts). It only
touches the two aortic stenosis outputs; every other head is passed through unchanged.
"""
from __future__ import annotations

import json
import os
from typing import Any, Dict, Optional, Sequence

AS_HEAD = "AVStenosis"
VMAX_HEAD = "AVPkVel(m|s)"
# Class order of the AVStenosis head (models/output_class_mapping.json).
MILD_MODERATE, NONE, SEVERE = 0, 1, 2

DEFAULT_PATH = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "models", "as_calibration.json")


def load_calibration(path: str = DEFAULT_PATH) -> Optional[Dict[str, Any]]:
    """Return the calibration, or None when the file is absent (then the 1.1.2 screening display applies)."""
    if not os.path.exists(path):
        return None
    with open(path, "r") as fp:
        cal = json.load(fp)
    for key in ("severe_threshold", "any_as_threshold", "vmax_map"):
        if key not in cal:
            raise ValueError(f"AS calibration {path} is missing {key!r}")
    if cal["vmax_map"].get("kind") != "linear":
        raise ValueError("only a linear AV Vmax map is supported")
    return cal


SIGNIFICANT = "Significant AS likely"
SIGNIFICANT_SEVERE = "Significant AS likely, severe possible"
NOT_SIGNIFICANT = "No significant AS suspected"


def calibrated_as_label(probabilities: Sequence[float], cal: Dict[str, Any]) -> str:
    """Primary MHI label: significant (moderate or severe) AS likely when P(Mild|Moderate)+P(Severe) >= t_any.

    The severe qualifier (P(Severe) >= t_severe) is added only when the calibration enables
    "severe_flag"; on MHI it did not separate severe from moderate AS well (specificity 0.74).
    """
    p_severe = float(probabilities[SEVERE])
    p_any = float(probabilities[MILD_MODERATE]) + p_severe
    if p_any < cal["any_as_threshold"]:
        return NOT_SIGNIFICANT
    if cal.get("severe_flag") and p_severe >= cal["severe_threshold"]:
        return SIGNIFICANT_SEVERE
    return SIGNIFICANT


def corrected_vmax(raw_vmax: float, cal: Dict[str, Any]) -> float:
    """Linear map of the raw AVPkVel output onto the MHI measured CW Doppler Vmax."""
    m = cal["vmax_map"]
    value = m["slope"] * float(raw_vmax) + m["intercept"]
    lo, hi = m.get("clip", [None, None])
    if lo is not None:
        value = max(lo, value)
    if hi is not None:
        value = min(hi, value)
    return float(value)
