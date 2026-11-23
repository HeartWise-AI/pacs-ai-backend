"""DeepCORO-JEPA 2.2.0 (model v19.2): occluded display, possible-stent annotation, FFR/iFR region and side check."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest
import torch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from logic import CustomPredictionService  # noqa: E402
from report_input import ResultError, predictions_of  # noqa: E402
from test_logic_units import GROUPS, SETTINGS, SHA, THRESHOLDS, _thresholds_file  # noqa: E402

EPICARDIAL = ["left_main", "prox_lad", "mid_lad", "dist_lad", "prox_lcx", "mid_lcx", "dist_lcx", "prox_rca", "mid_rca",
              "dist_rca", "pda", "posterolateral"]
V19_THRESHOLDS = {**THRESHOLDS, "values": {**THRESHOLDS["values"], "lvp_prob_significant": 0.0001},
                  "occluded": {"threshold": 0.5, "operating_point": "display_pi_050_epicardial", "segments": EPICARDIAL},
                  "stent_annotation": 0.0891}
SERVICE = CustomPredictionService.__new__(CustomPredictionService)


def _res(occluded=None, cto=None, stent=None, severity=None, prob=None, ffr_logit=-2.0, ifr_logit=-2.0):
    def vec(values, default):
        t = torch.full((18,), default)
        for g, x in (values or {}).items():
            t[GROUPS.index(g)] = x
        return t
    return {"encoder_passes": 2, "severity": vec(severity, 5.0), "binary_probability": vec(prob, 0.01),
            "physiology": {"ffr": torch.tensor([0.85]), "ifr": torch.tensor([0.93]),
                           "ffr_logit": torch.tensor([ffr_logit]), "ifr_logit": torch.tensor([ifr_logit])},
            "lesion_calcif_probability": vec(None, 0.001), "lesion_thrombus_probability": vec(None, 0.001),
            "lesion_cto_probability": vec(cto, 0.001), "occluded_probability": vec(occluded, 0.01),
            "stent_probability": vec(stent, 0.01)}


def _preds(**kw):
    return CustomPredictionService.postprocess(GROUPS, _res(**kw), n_videos=4, thresholds=V19_THRESHOLDS,
                                               model_version="v19", unsupported=("lvp",))


def test_load_thresholds_reads_the_occluded_display_point_and_the_stent_annotation(tmp_path):
    path = _thresholds_file(tmp_path)
    spec = json.loads((path / "thresholds.json").read_text())
    spec["thresholds"]["occluded_prob"] = {"display_95spec_epicardial": 0.22737, "display_pi_050_epicardial": 0.5}
    (path / "thresholds.json").write_text(json.dumps(spec))
    settings = {**SETTINGS, "occluded_operating_point": "display_pi_050_epicardial", "occluded_segments": EPICARDIAL,
                "stent_annotation_threshold": 0.0891}
    out = CustomPredictionService.load_thresholds(path, settings, GROUPS, physiology=True, lesion=True,
                                                  weights_sha256=SHA, occluded=True, procedure=True)
    assert out["occluded"] == {"threshold": 0.5, "operating_point": "display_pi_050_epicardial",
                               "segments": [g for g in GROUPS if g in EPICARDIAL]}
    assert out["stent_annotation"] == 0.0891
    with pytest.raises(ValueError, match="no occluded_prob threshold 'display_x'"):
        CustomPredictionService.load_thresholds(path, {**settings, "occluded_operating_point": "display_x"}, GROUPS,
                                                physiology=True, lesion=True, weights_sha256=SHA, occluded=True)
    with pytest.raises(ValueError, match="occluded_segments"):
        CustomPredictionService.load_thresholds(path, {**settings, "occluded_segments": ["nope"]}, GROUPS,
                                                physiology=True, lesion=True, weights_sha256=SHA, occluded=True)
    with pytest.raises(ValueError, match="procedure head"):
        CustomPredictionService.load_thresholds(path, settings, GROUPS, physiology=True, lesion=True,
                                                weights_sha256=SHA, occluded=True, procedure=False)


def test_the_shipped_configuration_uses_the_v19_2_display_point_and_the_v19_stent_threshold():
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))["Thresholds"]
    assert config["occluded_operating_point"] == "display_pi_050_epicardial" and config["occluded_segments"] == EPICARDIAL
    assert config["stent_annotation_threshold"] == 0.0891 and config["lesion_threshold_overrides"] == {"thrombus": 0.4}


def test_occluded_is_shown_100_on_epicardial_segments_only_and_tagged_by_the_cto_flag():
    preds = _preds(occluded={"prox_lad": 0.8, "mid_rca": 0.51, "D1": 0.95, "om1": 0.99, "dist_lad": 0.5},
                   cto={"mid_rca": 0.3}, severity={"prox_lad": 55.0})
    segs = preds["segments"]
    assert preds["occluded_threshold"] == {"threshold": 0.5, "operating_point": "display_pi_050_epicardial", "segments": EPICARDIAL}
    assert (segs["prox_lad"]["occluded"], segs["prox_lad"]["occlusion_tag"]) == (True, "acute/other")
    assert (segs["mid_rca"]["occluded"], segs["mid_rca"]["occlusion_tag"]) == (True, "CTO")
    assert not segs["D1"]["occluded"] and not segs["om1"]["occluded"] and segs["D1"]["occlusion_tag"] == ""  # branches
    assert not segs["dist_lad"]["occluded"]  # strictly greater than the threshold
    assert (segs["prox_lad"]["display_pct"], segs["prox_lad"]["display_category"]) == (100, "occluded")
    assert segs["prox_lad"]["stenosis_pct"] == 55.0  # the conditional estimate stays in the JSON
    assert not segs["prox_lad"]["significant"]  # no stenosis call changes
    text = SERVICE.diagnosis_text(preds)
    assert "Proximal LAD (P 0.010, shown 100% (occluded) - acute/other, P(occluded) 0.80)" in text
    assert "Mid RCA (P 0.010, shown 100% (occluded) - CTO, P(occluded) 0.51)" in text
    html = SERVICE.render_html(preds)
    assert "Prox LAD 100% (occluded)" in html and '<span class="chip occ">CTO</span>' in html
    assert html.count('class="cto-mark"') == 2  # drawn blocked: prox LAD and mid RCA


def test_in_v19_a_cto_flag_alone_is_a_tag_not_a_100_percent_display():
    preds = _preds(cto={"mid_lad": 0.4}, severity={"mid_lad": 30.0})
    v = preds["segments"]["mid_lad"]
    assert v["cto"] and not v["occluded"] and v["display_pct"] == 43 and v["display_category"] == "moderate"
    html = SERVICE.render_html(preds)
    assert 'class="cto-mark"' not in html and "Mid LAD 43%" in html


def test_possible_stent_annotates_and_changes_no_flag_or_grade():
    preds = _preds(stent={"prox_lad": 0.2, "mid_rca": 0.0891}, severity={"prox_lad": 50.0}, prob={"prox_lad": 0.9})
    segs = preds["segments"]
    assert preds["stent_annotation"] == {"threshold": 0.0891}
    assert segs["prox_lad"]["possible_stent"] and not segs["mid_rca"]["possible_stent"]
    assert segs["prox_lad"]["significant"] and segs["prox_lad"]["display_pct"] == 73
    text = SERVICE.diagnosis_text(preds)
    assert "shown 73%, model estimate 50%) (possible stent: grade may be unreliable after PCI)" in text
    recs = SERVICE.recommendations(preds)
    assert "do not upload them with diagnostic runs" in recs["en"] and "post-ICP" in recs["fr"]
    html = SERVICE.render_html(preds)
    assert '<span class="chip warn">Possible stent</span>' in html and "possible stent: grade may be unreliable after PCI" in html


def test_ffr_region_is_named_always_and_marked_when_ffr_or_ifr_is_abnormal():
    normal = _preds()
    ph = normal["lad_physiology"]
    assert ph["region"] == ["prox_lad", "mid_lad"] and ph["region_label"] == "proximal and mid LAD"
    assert set(ph["region_segments"]) == {"prox_lad", "mid_lad"}
    assert "LAD physiology (proximal-mid LAD): FFR predicted normal" in SERVICE.diagnosis_text(normal)
    assert not re.search(r'<li class="seg[^"]*is-region', SERVICE.render_html(normal))
    abnormal = _preds(ffr_logit=2.0, severity={"mid_lad": 60.0}, prob={"mid_lad": 0.9})
    assert abnormal["lad_physiology"]["ffr_abnormal"]
    text = SERVICE.diagnosis_text(abnormal)
    assert ("Predicted abnormal FFR/iFR refers to the proximal-mid LAD (the measurement region in training, not a "
            "localisation of the culprit lesion by the model): proximal lad shown 0% (not flagged), mid lad shown 79% (flagged).") in text
    html = SERVICE.render_html(abnormal)
    assert len(re.findall(r'<li class="seg[^"]*is-region', html)) == 2 and html.count("FFR/iFR region</span>") == 2
    assert "IVA proximale et moyenne" in SERVICE.recommendations(abnormal)["fr"]


def _views(**labels):
    return {f"sop{i}": label for i, label in enumerate(labels.values())}


def test_side_check_hides_the_side_without_a_run_and_pda_follows_dominance():
    preds = _preds(severity={"mid_rca": 65.0}, prob={"mid_rca": 0.9})
    views = {"a": "Left Coronary", "b": "Left Coronary", "c": "Left Coronary"}
    CustomPredictionService.apply_side_check(preds, views, ["a", "b", "c"], dominance=None)
    check, segs = preds["side_check"], preds["segments"]
    assert check["available"] and (check["left_runs"], check["right_runs"], check["not_assessed_side"]) == (3, 0, "right")
    assert check["dominance_side"] == {"call": "right", "source": "default (right dominant)"}
    assert check["not_assessed_segments"] == ["prox_rca", "mid_rca", "dist_rca", "pda", "posterolateral", "lvp"]
    v = segs["mid_rca"]
    assert v["assessed"] is False and v["not_assessed_reason"] == "no right coronary run"
    assert not v["significant"] and "display_pct" not in v and v["stenosis_pct"] == 65.0
    assert "assessed" not in segs["prox_lad"] and preds["lad_physiology"].get("assessed") is None
    text = SERVICE.diagnosis_text(preds)
    assert "Mid RCA" not in text.split("Not assessed")[0]
    assert "Not assessed (no right coronary run among the 3 runs read): Proximal RCA, Mid RCA, Distal RCA, PDA, Posterolateral" in text
    html = SERVICE.render_html(preds)
    assert "Mid RCA 65" not in html and "Not assessed: no right coronary run" in html
    assert predictions_of({"predictions": json.loads(json.dumps(SERVICE.public(preds)))})  # stored result renders


def test_left_dominant_pda_stays_with_the_left_runs_and_no_left_run_drops_the_pressure_call():
    preds = _preds()
    CustomPredictionService.apply_side_check(preds, {"a": "Left Coronary"}, ["a"],
                                             dominance={"call": "left", "method": "majority"})
    assert "pda" not in preds["side_check"]["not_assessed_segments"]
    assert preds["side_check"]["not_assessed_segments"] == ["prox_rca", "mid_rca", "dist_rca"]
    preds = _preds(ffr_logit=2.0)
    CustomPredictionService.apply_side_check(preds, {"a": "Right Coronary"}, ["a"], dominance=None)
    assert preds["side_check"]["not_assessed_side"] == "left" and "pda" not in preds["side_check"]["not_assessed_segments"]
    ph = preds["lad_physiology"]
    assert ph["assessed"] is False and not ph["ffr_abnormal"] and ph["not_assessed_reason"] == "no left coronary run"
    assert "LAD physiology: not assessed (no left coronary run)." in SERVICE.diagnosis_text(preds)


def test_no_left_run_also_clears_the_copied_region_flags():
    preds = _preds(severity={"prox_lad": 80.0}, prob={"prox_lad": 0.95})
    assert preds["segments"]["prox_lad"]["significant"] and preds["lad_physiology"]["region_segments"]["prox_lad"]["significant"]
    CustomPredictionService.apply_side_check(preds, {"a": "Right Coronary"}, ["a"], dominance=None)
    public = json.loads(json.dumps(SERVICE.public(preds)))
    for g, reading in public["lad_physiology"]["region_segments"].items():
        assert reading["significant"] is False and reading["significant"] == public["segments"][g]["significant"]
        assert reading["stenosis_pct"] == public["segments"][g]["stenosis_pct"]  # the numbers stay as written


def test_side_check_is_off_without_labels_for_every_run_read_and_with_both_sides():
    for views, used, reason in ((None, ["a"], "view labels not provided"),
                                ({"a": "Left Coronary"}, ["a", "b"], "a run read has no left/right coronary view label"),
                                ({"a": "Left Coronary", "b": "Ventriculography"}, ["a", "b"], "a run read has no left/right coronary view label")):
        preds = _preds()
        CustomPredictionService.apply_side_check(preds, views, used, dominance=None)
        assert preds["side_check"] == {"available": False, "reason": reason}
        assert not any("assessed" in v for v in preds["segments"].values())
    preds = _preds()
    CustomPredictionService.apply_side_check(preds, {"a": "Left Coronary", "b": "Right Coronary"}, ["a", "b"], None)
    assert preds["side_check"] == {"available": True, "left_runs": 1, "right_runs": 1, "not_assessed_side": None}


def test_coronary_views_metadata_is_validated():
    req = lambda v: SimpleNamespace(additionalMetadata={"coronaryViews": v})
    assert CustomPredictionService.coronary_views(SimpleNamespace(additionalMetadata={})) is None
    assert CustomPredictionService.coronary_views(req([{"sopInstanceUID": "1.2", "viewLabel": "Left Coronary"},
                                                       {"sopInstanceUID": "1.3", "viewLabel": None}])) == {
        "1.2": "Left Coronary", "1.3": None}
    for bad in ([], "x", [{"viewLabel": "Left Coronary"}], [{"sopInstanceUID": 3, "viewLabel": "x"}],
                [{"sopInstanceUID": "1", "viewLabel": 4}]):
        with pytest.raises(ValueError):
            CustomPredictionService.coronary_views(req(bad))


def test_unsupported_lvp_never_shows_occluded_or_a_stent():
    preds = _preds(occluded={"lvp": 0.99}, stent={"lvp": 0.99})
    v = preds["segments"]["lvp"]
    assert v["unsupported"] and not v["occluded"] and not v["possible_stent"] and v["occlusion_tag"] == ""


def test_a_stored_v19_result_renders_again_identically_and_bad_values_are_refused():
    preds = _preds(occluded={"prox_lad": 0.8}, stent={"mid_lad": 0.3}, ffr_logit=2.0)
    CustomPredictionService.apply_side_check(preds, {"a": "Left Coronary"}, ["a"], None)
    stored = json.loads(json.dumps(SERVICE.public(preds)))
    assert SERVICE.report_html(predictions_of({"predictions": stored})) == SERVICE.render_html(preds)
    for path, value in ((("segments", "prox_lad", "occlusion_tag"), "maybe"),
                        (("segments", "prox_lad", "occluded_prob"), 1.5),
                        (("segments", "mid_lad", "possible_stent"), "yes"),
                        (("lad_physiology", "region"), ["dist_lad"]),
                        (("side_check", "not_assessed_side"), "both")):
        bad = json.loads(json.dumps(stored))
        node = bad
        for key in path[:-1]:
            node = node[key]
        node[path[-1]] = value
        with pytest.raises(ResultError):
            predictions_of({"predictions": bad})
    tampered = json.loads(json.dumps(stored))
    tampered["segments"]["prox_lad"]["display_category"] = "moderate"
    with pytest.raises(ResultError, match="does not match display_pct"):
        predictions_of({"predictions": tampered})
