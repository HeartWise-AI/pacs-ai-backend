"""Checks a stored JSON-mode result before /inference/render-report draws it again. Pure Python (no torch).

The report is rebuilt from what an earlier predict returned (`data` of the JSON response, as stored by the caller),
so every value the renderer reads is checked here: type, range, known segment names and JPEG base64 images (they go
into data: URIs unescaped). A failure raises ResultError with the path of the first bad value.

Results of every version from 1.1.0 are accepted. 2.0.0 (model v15) adds `model_version`, `unsupported_segments`,
`study_lesions`, `model_dominance` and, on an unsupported segment, `unsupported: true` without display fields; all
are optional here so that older results still render. A result without `model_version` is a v13 result. A result of
a model version this container has no display mapping for is refused instead of being drawn with another mapping.
2.2.0 (model v19) adds, all optional: per segment `occluded_prob`, `occluded`, `occlusion_tag`, `stent_prob`,
`possible_stent`, `assessed` and `not_assessed_reason`; `occluded_threshold`, `stent_annotation`, `side_check`;
`lad_physiology.region`, `.region_label`, `.region_segments`, `.assessed`, `.not_assessed_reason`.
"""
from __future__ import annotations

import base64
import binascii
import math
import re

from display_calibration import CATEGORY_LABELS, calibrations, category, shown_occluded
from report_html import LESION_NAMES, SEGMENT_NAMES

OCCLUSION_TAGS = ("", "CTO", "acute/other")
SIDES = ("left", "right")

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


def _segments(segs, cuts: dict, model_version: str | None = None) -> None:
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
        if "unsupported" in v:  # 2.0.0: an output the model has no training target for (v15: lvp)
            _bool(v["unsupported"], f"{p}.unsupported")
        if "display_pct" in v:  # 1.4.0 and later; the report shows the stored value
            shown = _num(v["display_pct"], f"{p}.display_pct", 0.0, 100.0)
            if shown != int(shown):
                _fail(f"{p}.display_pct", "must be a whole number")
        if "display_category" in v:
            if v["display_category"] not in CATEGORY_LABELS:
                _fail(f"{p}.display_category", f"must be one of {tuple(CATEGORY_LABELS)}")
            if "display_pct" in v and v["display_category"] != category(v["display_pct"], shown_occluded(v, model_version)):
                _fail(f"{p}.display_category", "does not match display_pct")
        if "significant_threshold_pct" in v:
            _num(v["significant_threshold_pct"], f"{p}.significant_threshold_pct", 0.0, 100.0)
        for k in LESION_NAMES:
            if k in cuts or f"{k}_prob" in v or k in v:
                _prob(v.get(f"{k}_prob"), f"{p}.{k}_prob")
                _bool(v.get(k), f"{p}.{k}")
        if "occluded" in v or "occluded_prob" in v:  # 2.2.0 (v19)
            _prob(v.get("occluded_prob"), f"{p}.occluded_prob")
            _bool(v.get("occluded"), f"{p}.occluded")
            if v.get("occlusion_tag", "") not in OCCLUSION_TAGS or bool(v.get("occlusion_tag")) != v["occluded"]:
                _fail(f"{p}.occlusion_tag", "must be CTO or acute/other on an occluded segment, empty otherwise")
        if "possible_stent" in v or "stent_prob" in v:
            _prob(v.get("stent_prob"), f"{p}.stent_prob")
            _bool(v.get("possible_stent"), f"{p}.possible_stent")
        if "assessed" in v:
            if _bool(v["assessed"], f"{p}.assessed") is False:
                _text(v.get("not_assessed_reason"), f"{p}.not_assessed_reason", 64)


def _physiology(ph) -> None:
    p = "predictions.lad_physiology"
    _dict(ph, p)
    for k in ("ffr", "ifr"):
        _num(ph.get(k), f"{p}.{k}", -10.0, 10.0)
        _prob(ph.get(f"{k}_abnormal_prob"), f"{p}.{k}_abnormal_prob")
        _prob(ph.get(f"{k}_abnormal_threshold"), f"{p}.{k}_abnormal_threshold")
        _bool(ph.get(f"{k}_abnormal"), f"{p}.{k}_abnormal")
    _text(ph.get("operating_point"), f"{p}.operating_point", 64)
    if "region" in ph:  # 2.2.0
        if ph["region"] != ["prox_lad", "mid_lad"]:
            _fail(f"{p}.region", 'must be ["prox_lad", "mid_lad"]')
        _text(ph.get("region_label"), f"{p}.region_label", 64)
        for g, x in _dict(ph.get("region_segments", {}), f"{p}.region_segments").items():
            if g not in ("prox_lad", "mid_lad"):
                _fail(f"{p}.region_segments.{g}", "unknown segment")
            _dict(x, f"{p}.region_segments.{g}")
    if "assessed" in ph and _bool(ph["assessed"], f"{p}.assessed") is False:
        _text(ph.get("not_assessed_reason"), f"{p}.not_assessed_reason", 64)


def _lesion_thresholds(cuts) -> None:
    p = "predictions.lesion_thresholds"
    _dict(cuts, p)
    _text(cuts.get("operating_point"), f"{p}.operating_point", 64)
    for k in set(cuts) - {"operating_point"}:
        if k not in LESION_NAMES:
            _fail(f"{p}.{k}", "unknown lesion")
        _prob(cuts[k], f"{p}.{k}")


def _occluded_threshold(occ) -> None:
    p = "predictions.occluded_threshold"
    _dict(occ, p)
    _prob(occ.get("threshold"), f"{p}.threshold")
    _text(occ.get("operating_point"), f"{p}.operating_point", 64)
    if not isinstance(occ.get("segments"), list) or any(g not in SEGMENT_NAMES for g in occ["segments"]):
        _fail(f"{p}.segments", "must be a list of segment names")


def _side_check(check, segs: dict) -> None:
    p = "predictions.side_check"
    _dict(check, p)
    if not _bool(check.get("available"), f"{p}.available"):
        if "reason" in check:
            _text(check["reason"], f"{p}.reason")
        return
    _int(check.get("left_runs"), f"{p}.left_runs")
    _int(check.get("right_runs"), f"{p}.right_runs")
    side = check.get("not_assessed_side")
    if side is not None and side not in SIDES:
        _fail(f"{p}.not_assessed_side", "must be left, right or null")
    for i, g in enumerate(check.get("not_assessed_segments", [])):
        if g not in segs:
            _fail(f"{p}.not_assessed_segments[{i}]", "unknown segment")


def _model_version(version) -> None:
    p = "predictions.model_version"
    _text(version, p, 16)
    if version not in calibrations():
        _fail(p, f"no display calibration for this model version (known: {sorted(calibrations())})")


def _unsupported_segments(names, segs: dict) -> None:
    p = "predictions.unsupported_segments"
    if not isinstance(names, list) or len(names) > len(SEGMENT_NAMES):
        _fail(p, "must be a list of segment names")
    for i, g in enumerate(names):
        if g not in SEGMENT_NAMES:
            _fail(f"{p}[{i}]", "unknown segment")


def _study_lesions(study) -> None:
    p = "predictions.study_lesions"
    _dict(study, p)
    for k in study:
        if k not in {f"study_{name}_prob" for name in LESION_NAMES}:
            _fail(f"{p}.{k}", "unknown key")
        _prob(study[k], f"{p}.{k}")


def _model_dominance(dom) -> None:
    p = "predictions.model_dominance"
    _dict(dom, p)
    _prob(dom.get("dominance_left_prob"), f"{p}.dominance_left_prob")
    _prob(dom.get("dominance_left_threshold"), f"{p}.dominance_left_threshold")
    _bool(dom.get("dominance_left_pred"), f"{p}.dominance_left_pred")
    if "operating_point" in dom:
        _text(dom["operating_point"], f"{p}.operating_point", 64)
    if "label_source" in dom:
        _text(dom["label_source"], f"{p}.label_source")


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
    if "model_version" in preds:  # 2.0.0 and later; absent means model v13
        _model_version(preds["model_version"])
    _int(preds.get("n_videos"), "predictions.n_videos", 1)
    _int(preds.get("encoder_passes"), "predictions.encoder_passes")
    cuts = preds.get("lesion_thresholds")
    if cuts is not None:
        _lesion_thresholds(cuts)
    _segments(preds.get("segments"), cuts or {}, str(preds.get("model_version") or "v13"))
    if preds.get("lad_physiology") is not None:
        _physiology(preds["lad_physiology"])
    if preds.get("unsupported_segments") is not None:
        _unsupported_segments(preds["unsupported_segments"], preds["segments"])
    if preds.get("study_lesions") is not None:
        _study_lesions(preds["study_lesions"])
    if preds.get("model_dominance") is not None:
        _model_dominance(preds["model_dominance"])
    if preds.get("coronary_dominance") is not None:
        _dominance(preds["coronary_dominance"])
    if preds.get("occluded_threshold") is not None:
        _occluded_threshold(preds["occluded_threshold"])
    if preds.get("stent_annotation") is not None:
        _prob(_dict(preds["stent_annotation"], "predictions.stent_annotation").get("threshold"),
              "predictions.stent_annotation.threshold")
    if preds.get("side_check") is not None:
        _side_check(preds["side_check"], preds["segments"])
    if preds.get("explainability") is not None:
        _explainability(preds["explainability"], preds["segments"])
    return preds
