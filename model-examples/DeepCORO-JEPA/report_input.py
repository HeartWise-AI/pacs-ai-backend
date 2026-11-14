"""Checks a stored JSON-mode result before /inference/render-report draws it again. Pure Python (no torch).

The report is rebuilt from what an earlier predict returned (`data` of the JSON response, as stored by the caller),
so every value the renderer reads is checked here: type, range, known segment names and JPEG base64 images (they go
into data: URIs unescaped). A failure raises ResultError with the path of the first bad value.
"""
from __future__ import annotations

import base64
import binascii
import math
import re

from report_html import LESION_NAMES, SEGMENT_NAMES

DOMINANCE_METHODS = ("majority", "tie_mean_probability")
MAX_EXPLAINED = 18
MAX_IMAGE_BASE64 = 2_000_000
BASE64 = re.compile(r"[A-Za-z0-9+/]+={0,2}")


class ResultError(ValueError):
    pass


def _fail(path: str, why: str):
    raise ResultError(f"{path}: {why}")


def _dict(x, path: str) -> dict:
    if not isinstance(x, dict):
        _fail(path, "must be an object")
    return x


def _bool(x, path: str) -> bool:
    if not isinstance(x, bool):
        _fail(path, "must be true or false")
    return x


def _int(x, path: str, lo: int = 0) -> int:
    if isinstance(x, bool) or not isinstance(x, int) or x < lo:
        _fail(path, f"must be an integer >= {lo}")
    return x


def _num(x, path: str, lo: float | None = None, hi: float | None = None) -> float:
    if isinstance(x, bool) or not isinstance(x, (int, float)) or not math.isfinite(x):
        _fail(path, "must be a finite number")
    if (lo is not None and x < lo) or (hi is not None and x > hi):
        _fail(path, f"must be within [{lo}, {hi}]")
    return x


def _prob(x, path: str) -> float:
    return _num(x, path, 0.0, 1.0)


def _text(x, path: str, limit: int = 200) -> str:
    if not isinstance(x, str) or len(x) > limit:
        _fail(path, f"must be a string of at most {limit} characters")
    return x


def _jpeg(x, path: str) -> str:
    if not isinstance(x, str) or not x or len(x) > MAX_IMAGE_BASE64 or not BASE64.fullmatch(x):
        _fail(path, "must be a base64 JPEG")
    try:
        head = base64.b64decode(x[:8], validate=True)
    except (binascii.Error, ValueError):
        _fail(path, "must be a base64 JPEG")
    if not head.startswith(b"\xff\xd8\xff"):
        _fail(path, "must be a base64 JPEG")
    return x


def _segments(segs, cuts: dict) -> None:
    _dict(segs, "predictions.segments")
    if not segs:
        _fail("predictions.segments", "must not be empty")
    for g, v in segs.items():
        p = f"predictions.segments.{g}"
        if g not in SEGMENT_NAMES:
            _fail(p, "unknown segment")
        _dict(v, p)
        _num(v.get("stenosis_pct"), f"{p}.stenosis_pct", 0.0, 100.0)
        _prob(v.get("prob_significant"), f"{p}.prob_significant")
        _prob(v.get("prob_threshold"), f"{p}.prob_threshold")
        _bool(v.get("significant"), f"{p}.significant")
        if "significant_threshold_pct" in v:
            _num(v["significant_threshold_pct"], f"{p}.significant_threshold_pct", 0.0, 100.0)
        for k in LESION_NAMES:
            if k in cuts or f"{k}_prob" in v or k in v:
                _prob(v.get(f"{k}_prob"), f"{p}.{k}_prob")
                _bool(v.get(k), f"{p}.{k}")


def _physiology(ph) -> None:
    p = "predictions.lad_physiology"
    _dict(ph, p)
    for k in ("ffr", "ifr"):
        _num(ph.get(k), f"{p}.{k}", -10.0, 10.0)
        _prob(ph.get(f"{k}_abnormal_prob"), f"{p}.{k}_abnormal_prob")
        _prob(ph.get(f"{k}_abnormal_threshold"), f"{p}.{k}_abnormal_threshold")
        _bool(ph.get(f"{k}_abnormal"), f"{p}.{k}_abnormal")
    _text(ph.get("operating_point"), f"{p}.operating_point", 64)


def _lesion_thresholds(cuts) -> None:
    p = "predictions.lesion_thresholds"
    _dict(cuts, p)
    _text(cuts.get("operating_point"), f"{p}.operating_point", 64)
    for k in set(cuts) - {"operating_point"}:
        if k not in LESION_NAMES:
            _fail(f"{p}.{k}", "unknown lesion")
        _prob(cuts[k], f"{p}.{k}")


def _dominance(dom) -> None:
    p = "predictions.coronary_dominance"
    _dict(dom, p)
    if dom.get("call") not in ("left", "right"):
        _fail(f"{p}.call", "must be left or right")
    if dom.get("method") not in DOMINANCE_METHODS:
        _fail(f"{p}.method", f"must be one of {DOMINANCE_METHODS}")
    left, right = _int(dom.get("left_votes"), f"{p}.left_votes"), _int(dom.get("right_votes"), f"{p}.right_votes")
    n = _int(dom.get("video_count"), f"{p}.video_count", 1)
    if left + right != n:
        _fail(p, "left_votes + right_votes must equal video_count")
    mean = dom.get("mean_left_probability")
    if mean is not None or dom["method"] != "majority":
        _prob(mean, f"{p}.mean_left_probability")
    if "source" in dom:
        _text(dom["source"], f"{p}.source")


def _explainability(items, segs: dict) -> None:
    p = "predictions.explainability"
    if not isinstance(items, list) or len(items) > MAX_EXPLAINED:
        _fail(p, f"must be a list of at most {MAX_EXPLAINED} items")
    for i, it in enumerate(items):
        q = f"{p}[{i}]"
        _dict(it, q)
        if it.get("segment") not in SEGMENT_NAMES:
            _fail(f"{q}.segment", "unknown segment")
        if it.get("max_full_view_attention") is not None:
            _prob(it["max_full_view_attention"], f"{q}.max_full_view_attention")
        if not _bool(it.get("has_crop"), f"{q}.has_crop"):
            continue
        _int(it.get("video"), f"{q}.video")
        _int(it.get("frame"), f"{q}.frame")
        _prob(it.get("bag_attention"), f"{q}.bag_attention")
        _prob(it.get("top10pct_token_share"), f"{q}.top10pct_token_share")
        if it.get("images") is not None:
            images = _dict(it["images"], f"{q}.images")
            _jpeg(images.get("crop"), f"{q}.images.crop")
            _jpeg(images.get("frame"), f"{q}.images.frame")


def predictions_of(result) -> dict:
    """The `predictions` object of a stored JSON-mode result, checked for rendering. `{}` is the no-video answer.
    Accepts the `data` object or the whole response envelope ({"success", "data": {...}})."""
    result = _dict(result, "result")
    if "predictions" not in result and isinstance(result.get("data"), dict):
        result = result["data"]
    if "predictions" not in result:
        _fail("result.predictions", "is missing")
    preds = _dict(result["predictions"], "predictions")
    if not preds:
        return preds
    _int(preds.get("n_videos"), "predictions.n_videos", 1)
    _int(preds.get("encoder_passes"), "predictions.encoder_passes")
    cuts = preds.get("lesion_thresholds")
    if cuts is not None:
        _lesion_thresholds(cuts)
    _segments(preds.get("segments"), cuts or {})
    if preds.get("lad_physiology") is not None:
        _physiology(preds["lad_physiology"])
    if preds.get("coronary_dominance") is not None:
        _dominance(preds["coronary_dominance"])
    if preds.get("explainability") is not None:
        _explainability(preds["explainability"], preds["segments"])
    return preds
