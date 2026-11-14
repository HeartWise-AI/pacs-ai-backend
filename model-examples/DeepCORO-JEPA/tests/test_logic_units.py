"""Unit tests that need neither weights nor a GPU."""

from __future__ import annotations

import sys
from pathlib import Path

import json
import re

import numpy as np
import pydicom
import pytest
import torch
from pydicom.dataset import Dataset

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from logic import CustomPredictionService, shown  # noqa: E402

GROUPS = ("prox_rca", "mid_rca", "dist_rca", "pda", "posterolateral", "left_main", "prox_lad", "mid_lad", "dist_lad",
          "D1", "D2", "prox_lcx", "mid_lcx", "dist_lcx", "om1", "om2", "bx", "lvp")


SHA = "c" * 64
LESION_CUTS = {"calcif_prob": 0.2225, "thrombus_prob": 0.0851, "cto_prob": 0.157}
THRESHOLDS = {"pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99",
              "values": {**{f"{g}_prob_significant": 0.2 for g in GROUPS}, "ffr_abnormal_prob": 0.4396,
                         "ifr_abnormal_prob": 0.6141, **LESION_CUTS}}
SETTINGS = {"path": "thresholds.json", "pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99"}


def _thresholds_file(tmp_path, **changes):
    table = {f"{g}_prob_significant": {"balanced": 0.1} for g in GROUPS}
    table["ffr_abnormal_prob"] = {"balanced": 0.4396, "high_sensitivity": 0.1831, "high_specificity": 0.6859}
    table["ifr_abnormal_prob"] = {"balanced": 0.6141, "high_sensitivity": 0.1136, "high_specificity": 0.8719}
    table["calcif_prob"] = {"balanced": 0.0161, "specificity_95": 0.0791, "specificity_99": 0.2225}
    table["thrombus_prob"] = {"balanced": 0.0042, "specificity_95": 0.0167, "specificity_99": 0.0851}
    table["cto_prob"] = {"balanced": 0.0076, "specificity_95": 0.0329, "specificity_99": 0.157}
    spec = {"weights_sha256": SHA, "thresholds": table}
    spec.update(changes)
    (tmp_path / "thresholds.json").write_text(json.dumps(spec), encoding="utf-8")
    return tmp_path


def _dicom(sop: str, series_time: str | None) -> Dataset:
    ds = Dataset()
    ds.StudyInstanceUID, ds.SOPInstanceUID = "1.2.3", sop
    if series_time is not None:
        ds.SeriesTime = series_time
    return ds


def test_series_time_chooses_runs_and_training_filename_order_feeds_them(monkeypatch):
    service = CustomPredictionService.__new__(CustomPredictionService)
    monkeypatch.setattr(CustomPredictionService, "get_model_info", classmethod(lambda cls: {"dicomUploadMax": 2}))
    dicoms = [_dicom("9", "101000"), _dicom("1", "100500"), _dicom("5", "100000"), _dicom("3", None)]
    chosen = service.select_videos(dicoms)
    # the two earliest runs by SeriesTime (SOP 5 and 1), then ordered as '<study>_<sop>' filenames
    assert [d.SOPInstanceUID for d in chosen] == ["1", "5"]


def test_dicom_conversion_is_the_training_mjpg_avi(tmp_path):
    ds = Dataset()
    ds.PhotometricInterpretation = "MONOCHROME2"
    ds.Rows, ds.Columns, ds.NumberOfFrames = 32, 48, 3
    ds.SamplesPerPixel, ds.BitsAllocated, ds.BitsStored, ds.HighBit, ds.PixelRepresentation = 1, 8, 8, 7, 0
    ds.PixelData = np.random.default_rng(0).integers(0, 255, (3, 32, 48), dtype=np.uint8).tobytes()
    ds.file_meta = pydicom.dataset.FileMetaDataset()
    ds.file_meta.TransferSyntaxUID = pydicom.uid.ExplicitVRLittleEndian
    out = CustomPredictionService.dicom_to_avi(ds, str(tmp_path / "x.avi"))
    import cv2
    cap = cv2.VideoCapture(out)
    assert out and int(cap.get(cv2.CAP_PROP_FRAME_COUNT)) == 3
    assert int(cap.get(cv2.CAP_PROP_FOURCC)).to_bytes(4, "little") == b"MJPG"


def test_postprocess_reports_every_segment_and_physiology():
    res = {"encoder_passes": 2, "severity": torch.linspace(0, 110, 18), "binary_probability": torch.full((18,), 0.25),
           "physiology": {"ffr": torch.tensor([0.82]), "ifr": torch.tensor([0.9]),
                          "ffr_logit": torch.tensor([0.0]), "ifr_logit": torch.tensor([2.0])}}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=7, thresholds=THRESHOLDS)
    assert list(preds["segments"]) == list(GROUPS) and preds["n_videos"] == 7 and preds["encoder_passes"] == 2
    assert preds["segments"]["lvp"]["stenosis_pct"] == 100.0  # clamped
    assert preds["segments"]["left_main"]["significant_threshold_pct"] == 50
    assert preds["segments"]["prox_lad"]["significant_threshold_pct"] == 70
    assert preds["lad_physiology"]["ffr_abnormal_prob"] == 0.5
    assert "calcif_prob" not in preds["segments"]["prox_rca"] and "lesion_thresholds" not in preds  # no lesion head
    # calls: probability strictly greater than the threshold
    assert all(v["significant"] and v["prob_threshold"] == 0.2 for v in preds["segments"].values())  # 0.25 > 0.2
    assert preds["lad_physiology"]["ffr_abnormal"] is True and preds["lad_physiology"]["ffr_abnormal_threshold"] == 0.4396
    assert preds["lad_physiology"]["ifr_abnormal"] is True and preds["lad_physiology"]["operating_point"] == "balanced"
    service = CustomPredictionService.__new__(CustomPredictionService)
    assert service.significant_segments(preds) == list(GROUPS)
    assert "FFR predicted abnormal" in service.diagnosis_text(preds) and "Flagged" in service.render_html(preds)


def test_a_probability_equal_to_the_threshold_is_not_a_positive_call():
    cut = {"pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99",
           "values": {**{f"{g}_prob_significant": 0.25 for g in GROUPS}, "ffr_abnormal_prob": 0.5, "ifr_abnormal_prob": 0.9,
                      "calcif_prob": 0.25, "thrombus_prob": 0.25, "cto_prob": 0.25}}
    res = {"encoder_passes": 2, "severity": torch.full((18,), 80.0), "binary_probability": torch.full((18,), 0.25),
           "physiology": {"ffr": torch.tensor([0.82]), "ifr": torch.tensor([0.9]),
                          "ffr_logit": torch.tensor([0.0]), "ifr_logit": torch.tensor([2.0])},
           **{f"lesion_{name}_probability": torch.full((18,), 0.25) for name in ("calcif", "thrombus", "cto")}}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=cut)
    # an estimated 80% stenosis alone does not flag a segment: the call is on the probability
    assert not any(v["significant"] or v["calcif"] or v["thrombus"] or v["cto"] for v in preds["segments"].values())
    assert preds["lad_physiology"]["ffr_abnormal"] is False and preds["lad_physiology"]["ifr_abnormal"] is False
    service = CustomPredictionService.__new__(CustomPredictionService)
    text = service.diagnosis_text(preds)
    assert text.startswith("No segment flagged") and "No segment flagged for chronic total occlusion." in text


def test_a_call_follows_the_returned_probability():
    """The response rounds probabilities to 6 decimals and a flag is taken on that returned value, so
    `flag == (probability > threshold)` holds for whoever reads the response."""
    cut = {"pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99",
           "values": {**{f"{g}_prob_significant": 0.25 for g in GROUPS}, "ffr_abnormal_prob": 0.5, "ifr_abnormal_prob": 0.5,
                      "calcif_prob": 0.25, "thrombus_prob": 0.25, "cto_prob": 0.25}}
    just_above, above = torch.full((18,), 0.2500004), torch.full((18,), 0.250006)  # returned as 0.25 and 0.250006
    res = {"encoder_passes": 2, "severity": torch.zeros(18), "binary_probability": just_above,
           "physiology": {"ffr": torch.tensor([0.82]), "ifr": torch.tensor([0.9]),
                          "ffr_logit": torch.tensor([0.000001]), "ifr_logit": torch.tensor([0.001])},  # P 0.50000025, 0.50025
           "lesion_calcif_probability": just_above, "lesion_thrombus_probability": above, "lesion_cto_probability": just_above}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=cut)
    for v in preds["segments"].values():
        assert v["prob_significant"] == 0.25 and v["significant"] is False
        assert (v["calcif_prob"], v["calcif"], v["thrombus_prob"], v["thrombus"]) == (0.25, False, 0.250006, True)
        assert (v["cto_prob"], v["cto"]) == (0.25, False)
    physiology = preds["lad_physiology"]
    assert (physiology["ffr_abnormal_prob"], physiology["ffr_abnormal"]) == (0.5, False)
    assert (physiology["ifr_abnormal_prob"], physiology["ifr_abnormal"]) == (0.50025, True)
    # the diagnosis and the report print few decimals, but never a number on the wrong side of the threshold
    service = CustomPredictionService.__new__(CustomPredictionService)
    text, html = service.diagnosis_text(preds), service.render_html(preds)
    assert "iFR predicted abnormal (P(iFR ≤ 0.89) 0.5002, threshold 0.5000)" in text
    assert "FFR predicted normal (P(FFR ≤ 0.80) 0.50, threshold 0.5000)" in text
    assert "Flagged for thrombus (P above 0.2500): Proximal RCA (P 0.25001)" in text
    row = re.search(r'<li class="seg"><span class="sname">Proximal RCA(.*?)</li>', html).group(1)  # not flagged
    assert '<span class="chip warn">Thrombus</span>' in row and 'class="pct sev-none" title="Normal; model estimate 0.0%"><span class="normal">0%</span></span>' in row
    assert 'P 0.250<span class="muted"> / 0.2500</span>' in row
    assert "Calcification 0.25 · Thrombus 0.25001 · CTO 0.25" in row


def test_printed_probabilities_keep_their_side_of_the_threshold():
    assert shown(0.812345, 0.1558, 3) == "0.812" and shown(0.258326, 0.2644, 2) == "0.26"
    assert shown(0.00064, 0.0006, 3) == "0.001"  # printed above 0.0006, as called
    assert shown(0.0004, 0.0006, 3) == "0.000" and shown(0.0006, 0.0006, 3) == "0.0006"  # 0.001 would read as flagged
    assert shown(0.223, 0.2225, 2) == "0.223"  # "0.22" would read as below the threshold of a positive call
    assert shown(0.264401, 0.2644, 2) == "0.264401" and shown(0.2644, 0.2644, 2) == "0.26"


def test_thresholds_are_read_per_output_and_operating_point(tmp_path):
    models_dir = _thresholds_file(tmp_path)
    got = CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, physiology=True, lesion=True, weights_sha256=SHA)
    assert got["values"]["ffr_abnormal_prob"] == 0.4396 and got["values"]["ifr_abnormal_prob"] == 0.6141
    assert {name: got["values"][name] for name in LESION_CUTS} == LESION_CUTS  # one threshold per finding
    assert len(got["values"]) == 23 and got["pressure_operating_point"] == "balanced"
    assert got["lesion_operating_point"] == "specificity_99"
    other = CustomPredictionService.load_thresholds(
        models_dir, {**SETTINGS, "pressure_operating_point": "high_sensitivity", "lesion_operating_point": "specificity_95"},
        GROUPS, physiology=True, lesion=True, weights_sha256=SHA)
    assert other["values"]["ifr_abnormal_prob"] == 0.1136 and other["values"]["prox_lad_prob_significant"] == 0.1
    assert other["values"]["thrombus_prob"] == 0.0167
    # a checkpoint without those heads needs no pressure or lesion thresholds
    assert len(CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, physiology=True, lesion=False, weights_sha256=SHA)["values"]) == 20
    assert len(CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, physiology=False, lesion=False, weights_sha256=SHA)["values"]) == 18


def test_thresholds_fail_loudly_instead_of_falling_back(tmp_path):
    models_dir = _thresholds_file(tmp_path)
    with pytest.raises(ValueError, match="Unknown pressure_operating_point"):
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "pressure_operating_point": "youden"}, GROUPS, physiology=True, lesion=True, weights_sha256=SHA)
    with pytest.raises(ValueError, match="Unknown lesion_operating_point"):  # checked even without a lesion head
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "lesion_operating_point": "high_sensitivity"}, GROUPS, physiology=True, lesion=False, weights_sha256=SHA)
    with pytest.raises(ValueError, match="takes exactly"):
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "operating_point": "balanced"}, GROUPS, physiology=True, lesion=True, weights_sha256=SHA)
    with pytest.raises(ValueError, match="takes exactly"):  # the 1.0.0 block, without lesion_operating_point
        CustomPredictionService.load_thresholds(
            models_dir, {"path": "thresholds.json", "pressure_operating_point": "balanced"}, GROUPS, physiology=True, lesion=True, weights_sha256=SHA)
    with pytest.raises(ValueError, match="was derived for weights"):
        CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, physiology=True, lesion=True, weights_sha256="d" * 64)
    with pytest.raises(ValueError, match="no threshold for"):
        CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS + ("new_segment",), physiology=True, lesion=True, weights_sha256=SHA)
    with pytest.raises(FileNotFoundError):
        CustomPredictionService.load_thresholds(tmp_path / "absent", SETTINGS, GROUPS, physiology=True, lesion=True, weights_sha256=SHA)
    stenosis_only = {f"{g}_prob_significant": {"balanced": 0.1} for g in GROUPS}
    with pytest.raises(ValueError, match=r"no threshold for \['calcif_prob', 'thrombus_prob', 'cto_prob'\]"):
        CustomPredictionService.load_thresholds(_thresholds_file(tmp_path, thresholds=stenosis_only), SETTINGS, GROUPS, physiology=False, lesion=True, weights_sha256=SHA)
    broken = _thresholds_file(tmp_path, thresholds={f"{g}_prob_significant": {"balanced": 1.5} for g in GROUPS})
    with pytest.raises(ValueError, match="outside"):
        CustomPredictionService.load_thresholds(broken, SETTINGS, GROUPS, physiology=False, lesion=False, weights_sha256=SHA)


def test_lesion_outputs_are_reported_and_flagged_when_the_checkpoint_has_them():
    cto = torch.zeros(18)
    cto[GROUPS.index("prox_rca")] = 0.4
    res = {"encoder_passes": 1, "severity": torch.zeros(18), "binary_probability": torch.zeros(18),
           "lesion_calcif_probability": torch.full((18,), 0.3), "lesion_thrombus_probability": torch.zeros(18),
           "lesion_cto_probability": cto}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=THRESHOLDS)
    assert preds["segments"]["mid_lad"]["calcif_prob"] == 0.3 and "lad_physiology" not in preds
    # one threshold per finding for all segments, reported once
    assert preds["lesion_thresholds"] == {"operating_point": "specificity_99", "calcif": 0.2225, "thrombus": 0.0851, "cto": 0.157}
    assert all(v["calcif"] and not v["thrombus"] for v in preds["segments"].values())  # 0.3 > 0.2225, 0 < 0.0851
    assert [g for g, v in preds["segments"].items() if v["cto"]] == ["prox_rca"]
    service = CustomPredictionService.__new__(CustomPredictionService)
    text = service.diagnosis_text(preds)
    assert "Flagged for chronic total occlusion (P above 0.1570): Proximal RCA (P 0.40)." in text
    assert "No segment flagged for thrombus." in text and "Flagged for calcification" in text
    html = service.render_html(preds)
    row = re.search(r'<li class="seg is-flag"><span class="sname">Proximal RCA(.*?)</li>', html).group(1)
    assert '<span class="chip warn">CTO</span>' in row and "CTO 0.40" in row
    # a CTO is reported as an occlusion: 100%, flagged, vessel blocked at the lesion and pale beyond it
    assert 'class="pct sev-high" title="Occluded; model estimate 0.0%">100%<span class="muted small"> est. 0%</span></span>' in row
    assert html.count('class="cto-mark"') == 1 and html.count('class="occluded"') == 1
    assert "Operating point specificity_99" in html and "Proximal RCA (100%, occluded)" in html
    assert "Proximal RCA (P 0.000, estimated 100%, CTO flag; stenosis estimate 0%)" not in text  # not significant


def test_thrombus_is_drawn_and_the_posterior_lv_branch_is_hidden():
    thrombus = torch.zeros(18)
    thrombus[GROUPS.index("mid_lad")] = 0.5
    thrombus[GROUPS.index("lvp")] = 0.5
    res = {"encoder_passes": 1, "severity": torch.zeros(18), "binary_probability": torch.full((18,), 0.25),
           "lesion_calcif_probability": torch.zeros(18), "lesion_thrombus_probability": thrombus,
           "lesion_cto_probability": torch.zeros(18)}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=THRESHOLDS)
    assert preds["segments"]["lvp"]["thrombus"] and preds["segments"]["lvp"]["significant"]  # still in the JSON
    service = CustomPredictionService.__new__(CustomPredictionService)
    html, text = service.render_html(preds), service.diagnosis_text(preds)
    assert html.count('class="thrombus"') == 1 and "Flagged for thrombus (P above 0.0851): Mid LAD (P 0.50)." in text
    assert "Posterior left ventricular" not in html and "PLV" not in html and "Posterior left ventricular" not in text
    assert "<b>17</b> segments shown" in html
    assert html.index('id="method"') > html.index('class="disclaimer"'), "method addendum closes the report"


def test_model_facts_name_the_training_run_of_the_served_weights():
    facts = json.loads((ROOT / "data/model_facts.json").read_text(encoding="utf-8"))["en"]
    info = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    assert "https://wandb.ai/mhi_ai/deepcoro-segmentation/runs/400d0a77" in facts["Summary"]["Training_run"]
    assert facts["Summary"]["Version"] == info["version"]


def _service_with_one_readable_clip(monkeypatch, measured_forward):
    """A service whose DICOM decoding succeeds, so only the model call decides the outcome."""
    import types
    monkeypatch.setitem(sys.modules, "integrated_data", types.SimpleNamespace(load_video_clip=lambda path, sampling: torch.zeros(16, 8, 8)))
    monkeypatch.setitem(sys.modules, "integrated_study", types.SimpleNamespace(measured_forward=measured_forward))
    monkeypatch.setattr(CustomPredictionService, "models", {"deepcoro_jepa": torch.nn.Linear(1, 1)}, raising=False)
    monkeypatch.setattr(CustomPredictionService, "model_config", {"Forward": {
        "full_res": 224, "crop_res": 320, "max_views": 12, "crops_per_group": 4, "frame_sampling": "peak_s2"}}, raising=False)
    monkeypatch.setattr(CustomPredictionService, "supported_output_modes", ["json"])
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "get_model_info", classmethod(lambda cls: {}))
    monkeypatch.setattr(CustomPredictionService, "_decode_dicoms", lambda self, request: [object()])
    monkeypatch.setattr(CustomPredictionService, "select_videos", lambda self, dicoms: dicoms)
    monkeypatch.setattr(CustomPredictionService, "dicom_to_avi", staticmethod(lambda dicom, output_path: output_path))
    return CustomPredictionService.__new__(CustomPredictionService)


def test_a_failed_forward_pass_is_an_error_not_a_no_video_report(monkeypatch):
    import asyncio
    import types

    def out_of_memory(model, clips, args):
        raise RuntimeError("CUDA out of memory")

    service = _service_with_one_readable_clip(monkeypatch, out_of_memory)
    with pytest.raises(RuntimeError, match="out of memory"):
        service._run_inference([object()])
    success, response = asyncio.run(service.predict(types.SimpleNamespace(outputMode="json")))
    body = json.loads(response.body)
    assert success is False and response.status_code == 500
    assert body["success"] is False and body["errorCode"] == "PROCESSING_ERROR" and "out of memory" in body["message"]


def test_no_usable_clip_is_still_reported_as_no_video(monkeypatch):
    import asyncio
    import types
    service = _service_with_one_readable_clip(monkeypatch, measured_forward=None)
    monkeypatch.setattr(CustomPredictionService, "dicom_to_avi", staticmethod(lambda dicom, output_path: None))
    success, result = asyncio.run(service.predict(types.SimpleNamespace(outputMode="json")))
    assert success is True and result["predictions"] == {} and result["modelRecommendations"]["presentable"] is False


def _dominance_request(**dom):
    from types import SimpleNamespace
    return SimpleNamespace(additionalMetadata={"coronaryDominance": dom} if dom else {})


VALID_DOMINANCE = {"call": "left", "method": "majority", "leftVotes": 3, "rightVotes": 1, "videoCount": 4,
                   "meanLeftProbability": 0.7, "source": "angiogram procedure view classifier 1.0.3"}


def test_coronary_dominance_comes_from_request_metadata_and_is_validated():
    assert CustomPredictionService.coronary_dominance(_dominance_request()) is None
    assert CustomPredictionService.coronary_dominance(_dominance_request(**VALID_DOMINANCE)) == {
        "call": "left", "method": "majority", "left_votes": 3, "right_votes": 1, "video_count": 4,
        "mean_left_probability": 0.7, "source": "angiogram procedure view classifier 1.0.3"}
    tie = {**VALID_DOMINANCE, "method": "tie_mean_probability", "leftVotes": 2, "rightVotes": 2, "meanLeftProbability": 0.6}
    assert CustomPredictionService.coronary_dominance(_dominance_request(**tie))["call"] == "left"
    for bad in ({"call": "codominant"}, {"call": "right"}, {"leftVotes": 2}, {"videoCount": 0, "leftVotes": 0, "rightVotes": 0},
                {"meanLeftProbability": 1.5}, {"method": "tie_mean_probability"}, {"leftVotes": True},
                {**tie, "call": "right", "method": "tie_mean_probability", "leftVotes": 2, "rightVotes": 2}):
        with pytest.raises(ValueError, match="coronaryDominance"):
            CustomPredictionService.coronary_dominance(_dominance_request(**{**VALID_DOMINANCE, **bad}))


def test_dominance_is_reported_and_redraws_the_schema():
    res = {"encoder_passes": 1, "severity": torch.zeros(18), "binary_probability": torch.zeros(18)}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=4, thresholds=THRESHOLDS)
    service = CustomPredictionService.__new__(CustomPredictionService)
    html = service.render_html(preds)
    assert "Not provided" in html and 'aria-label="Right-dominant' in html and "Coronary dominance" not in service.diagnosis_text(preds)
    preds["coronary_dominance"] = CustomPredictionService.coronary_dominance(_dominance_request(**VALID_DOMINANCE))
    html = service.render_html(preds)
    assert "Left dominant" in html and 'aria-label="Left-dominant' in html and "3 of 4 diagnostic videos" in html
    assert service.diagnosis_text(preds).endswith(
        "Coronary dominance: left (angiogram classifier, majority of 4 diagnostic videos: 3 left, 1 right).")


def test_explanations_reproduce_the_within_crop_attention_and_keep_their_images_in_the_json():
    import base64 as b64
    from types import SimpleNamespace
    from explain import capture_pooling, explain_segments
    from integrated_model import CropSpec

    side, slots, d = 20, 8, 8
    gi = GROUPS.index("mid_rca")
    q = torch.zeros(18, d)
    q[gi, 0] = 1.0
    tokens = torch.zeros(1, slots * side * side, d)
    spike = 3 * side * side + 5 * side + 12  # slot 3, row 5, column 12
    tokens[0, spike, 0] = 400.0
    boxes = torch.tensor([[10.0, 20.0, 40.0, 40.0]]).repeat(16, 1)
    bag = [torch.tensor([1.0]) for _ in GROUPS]
    bag[gi] = torch.tensor([0.7, 0.2, 0.1])  # one crop, two full views
    store = {"specs": [CropSpec(0, gi, boxes, 50.0)], "crop_tokens": tokens,
             "crop_fractions": torch.full((1, slots * side * side), 0.5), "bag_attention": bag}
    model = SimpleNamespace(groups=GROUPS, q=q, mask_w=torch.ones(18))
    items = explain_segments(model, store, [torch.rand(16, 64, 80)], ["mid_rca", "D2"], crop_res=320)
    crop, none = items
    assert none == {"segment": "D2", "has_crop": False, "max_full_view_attention": 1.0}
    assert crop["frame"] == 6 and crop["bag_attention"] == 0.7 and crop["max_full_view_attention"] == 0.2
    assert crop["peak_in_crop_xy"] == [round(12.5 / side, 3), round(5.5 / side, 3)]
    assert crop["top10pct_token_share"] > 0.99
    assert b64.b64decode(crop["images"]["crop"])[:2] == b"\xff\xd8"  # JPEG

    class Tiny(torch.nn.Module):
        def _pool_study(self, *args):
            return "pred", "features", ["attention"]
    tiny = Tiny()
    with capture_pooling(tiny) as seen:
        assert tiny._pool_study(1, 2, 3, 4, [5]) == ("pred", "features", ["attention"])
    assert seen["crop_tokens"] == 3 and seen["specs"] == [5] and "_pool_study" not in tiny.__dict__

    res = {"encoder_passes": 2, "severity": torch.full((18,), 80.0), "binary_probability": torch.zeros(18)}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=THRESHOLDS)
    preds["explainability"] = items
    public = CustomPredictionService.public({**preds, "explainability": [{**items[0], "_images": {}}, items[1]]})
    assert "_images" not in public["explainability"][0]
    assert public["explainability"][0]["images"] == items[0]["images"]  # kept in the JSON since 1.3.0
    json.dumps(public)  # JSON-safe
    html = CustomPredictionService.__new__(CustomPredictionService).render_html(preds)
    assert "Where the model looked" in html and html.count("data:image/jpeg;base64,") == 2
    assert "No tracked crop for this segment" in html and "This crop carried 70% of the segment's attention" in html
