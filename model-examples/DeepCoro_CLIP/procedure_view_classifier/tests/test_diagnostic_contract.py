"""Synthetic classifier contract tests; no weights, GPU or patient data."""

import asyncio
import hashlib
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest
import torch
from pydicom.dataset import Dataset

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from logic import CustomPredictionService
from utils.diagnostic_contract import verified_preprocessing_contract


def prediction(uid, timestamp, stent="absent", main="Left Coronary", contrast="yes"):
    return {"sop_instance_uid": uid, "series_time": timestamp, "stent_presence": stent,
            "main_structure": main, "contrast_agent": contrast}


def test_complete_study_chronology_accepts_times_without_fraction():
    service = CustomPredictionService()
    rows = [prediction("3", "12:02:00"), prediction("1", "12:00:00"),
            prediction("2", "12:01:00.123", "present"),
            prediction("4", "12:03:00", main="Right Coronary")]
    result = service._assign_procedure_status(rows)
    assert [r["sop_instance_uid"] for r in result] == ["1", "2", "3", "4"]
    assert [r["status"] for r in result] == ["diagnostic", "PCI", "POST_PCI", "diagnostic"]


@pytest.mark.parametrize("timestamp", [None, "", "12:00", "120000", "25:00:00"])
def test_incomplete_or_invalid_time_does_not_become_diagnostic(timestamp):
    with pytest.raises(ValueError):
        CustomPredictionService()._assign_procedure_status([prediction("1", timestamp)])


def test_ambiguous_tied_pci_timing_fails_closed():
    with pytest.raises(ValueError, match="Ambiguous PCI timing"):
        CustomPredictionService()._assign_procedure_status([
            prediction("1", "12:00:00", "present"), prediction("2", "12:00:00")])


def _batch_service(monkeypatch, forward_batch_size, preprocess_workers=1):
    service = CustomPredictionService()
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    monkeypatch.setattr(CustomPredictionService, "config", SimpleNamespace(
        forward_batch_size=forward_batch_size, preprocess_workers=preprocess_workers,
        head_structure=SimpleNamespace(main_structure=2, contrast_agent=1, stent_presence=1)), raising=False)
    monkeypatch.setattr(CustomPredictionService, "class_mapping", {
        "main_structure": {0: "Left Coronary", 1: "Right Coronary"},
        "contrast_agent": {0: "no", 1: "yes"},
        "stent_presence": {0: "absent", 1: "present"}}, raising=False)
    calls = []

    def model(batch):
        # every video is filled with its own index, so a row's output identifies the video it came from
        calls.append(tuple(batch.shape))
        index = batch[:, 0, 0, 0, 0]
        return {"main_structure": torch.stack([-index, index - 2.5], dim=1),  # Right Coronary from the 3rd video on
                "contrast_agent": (index % 2 * 2 - 1).unsqueeze(1),            # yes for odd indices
                "stent_presence": torch.full((batch.shape[0], 1), -1.)}

    monkeypatch.setattr(CustomPredictionService, "models", {"vaso_vision": model})
    monkeypatch.setattr(service, "_process_dicom", lambda d: torch.full((3, 2, 4, 4), float(d.Index)))
    dicoms = []
    for i in range(5):
        d = Dataset()
        d.SeriesInstanceUID, d.SOPInstanceUID, d.SeriesTime, d.Index = "1.2.3", f"1.2.3.{i+1}", f"12000{i}", i
        dicoms.append(d)
    return service, dicoms, calls


@pytest.mark.parametrize("size, workers, shapes", [
    (1, 1, [(1, 3, 2, 4, 4)] * 5),
    (2, 1, [(2, 3, 2, 4, 4), (2, 3, 2, 4, 4), (1, 3, 2, 4, 4)]),
    (8, 4, [(5, 3, 2, 4, 4)]),
    (1, 8, [(1, 3, 2, 4, 4)] * 5),  # the shipped setting: parallel preparation, one video per forward
    (2, 3, [(2, 3, 2, 4, 4), (2, 3, 2, 4, 4), (1, 3, 2, 4, 4)]),
])
def test_forward_batches_keep_request_order_and_sop_identity(monkeypatch, size, workers, shapes):
    service, dicoms, calls = _batch_service(monkeypatch, size, workers)
    results = service._run_inference(dicoms, ["1.2.3"] * 5)
    assert calls == shapes
    assert [r["sop_instance_uid"] for r in results] == [f"1.2.3.{i+1}" for i in range(5)]
    assert all(r["series_instance_uid"] == r["dicom_name"] == "1.2.3" for r in results)
    # the same labels whatever the batch size: each row is read from its own video
    assert [r["main_structure"] for r in results] == ["Left Coronary"] * 2 + ["Right Coronary"] * 3
    assert [r["contrast_agent"] for r in results] == ["no", "yes", "no", "yes", "no"]
    assert [r["series_time"] for r in results] == [f"12:00:0{i}" for i in range(5)]


def test_a_model_returning_fewer_rows_than_videos_fails(monkeypatch):
    service, dicoms, _ = _batch_service(monkeypatch, 8)
    full = CustomPredictionService.models["vaso_vision"]
    monkeypatch.setattr(CustomPredictionService, "models", {"vaso_vision": lambda b: {k: v[:1] for k, v in full(b).items()}})
    with pytest.raises(ValueError, match="different number of predictions"):
        service._run_inference(dicoms, ["1.2.3"] * 5)


def test_declared_forward_batch_size_is_the_configured_one():
    import json
    from utils.http_utils import Config
    declared = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))["preprocessingContract"]
    assert declared["forwardBatchSize"] == Config.model_fields["forward_batch_size"].default == 1
    assert Config.model_fields["preprocess_workers"].default == 8
    assert 'declared_contract.get("forwardBatchSize") != config.forward_batch_size' in (ROOT / "main.py").read_text(encoding="utf-8")


def test_dominance_returns_label_and_left_probability(monkeypatch):
    service = CustomPredictionService()
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    monkeypatch.setattr(CustomPredictionService, "config", SimpleNamespace(head_structure=SimpleNamespace(
        main_structure=2, contrast_agent=1, stent_presence=1, dominance=1)), raising=False)
    monkeypatch.setattr(CustomPredictionService, "class_mapping", {
        "main_structure": {0: "Left Coronary", 1: "Right Coronary"}, "contrast_agent": {0: "no", 1: "yes"},
        "stent_presence": {0: "absent", 1: "present"},
        "dominance": {0: "right_dominant", 1: "left_dominant"}}, raising=False)
    logits = iter([-2.0, 0.0])  # P(left) 0.119 -> right; P(left) 0.5 -> left (threshold unchanged at >= 0.5)
    monkeypatch.setattr(CustomPredictionService, "models", {"vaso_vision": lambda batch: {
        "main_structure": torch.tensor([[2., 0.]]), "contrast_agent": torch.tensor([[1.]]),
        "stent_presence": torch.tensor([[-1.]]), "dominance": torch.tensor([[next(logits)]])}})
    monkeypatch.setattr(service, "_process_dicom", lambda _: torch.zeros(3, 2, 4, 4))
    dicoms = []
    for i in range(2):
        d = Dataset()
        d.SeriesInstanceUID, d.SOPInstanceUID, d.SeriesTime = "1.2.3", f"1.2.3.{i+1}", f"12000{i}"
        dicoms.append(d)
    results = service._run_inference(dicoms, ["1.2.3"] * 2)
    assert [r["dominance"] for r in results] == ["right_dominant", "left_dominant"]
    assert [r["dominance_left_probability"] for r in results] == [0.119203, 0.5]


def test_invalid_input_cannot_return_a_partial_success():
    with pytest.raises(ValueError, match="valid, uniquely identified"):
        asyncio.run(CustomPredictionService()._handle_json_output(
            SimpleNamespace(seriesInstanceImages={0: {0: "invalid-base64"}})))


def test_unknown_head_labels_cannot_be_assigned_diagnostic_status():
    row = prediction("1", "12:00:00")
    row["stent_presence"] = None
    with pytest.raises(ValueError, match="complete procedure label"):
        CustomPredictionService()._assign_procedure_status([row])


def test_loaded_model_is_not_loaded_again(monkeypatch):
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    CustomPredictionService().load_model(None)


def test_artifact_contract_requires_actual_hashes_and_pinned_source(tmp_path):
    models = tmp_path / "models"
    models.mkdir()
    (models / "weights.pt").write_bytes(b"synthetic weights")
    (models / "mapping.json").write_bytes(b"synthetic mapping")
    contract = {"schemaVersion": "angiogram-procedure-view-sop-v1",
                "weightsSha256": hashlib.sha256(b"synthetic weights").hexdigest(),
                "classMappingSha256": hashlib.sha256(b"synthetic mapping").hexdigest()}
    verified = verified_preprocessing_contract(tmp_path, contract, "weights.pt", "mapping.json", "a" * 40)
    assert verified["sourceRevision"] == "a" * 40
    (models / "weights.pt").write_bytes(b"changed")
    with pytest.raises(ValueError, match="does not match"):
        verified_preprocessing_contract(tmp_path, contract, "weights.pt", "mapping.json", "a" * 40)
    with pytest.raises(ValueError, match="source revision"):
        verified_preprocessing_contract(tmp_path, contract, "weights.pt", "mapping.json", None)


def test_json_response_keeps_labels_as_text_and_the_dominance_probability_as_a_number():
    from utils.http_utils import JsonPredictionResponse
    row = {**prediction("1", "12:00:00"), "status": "diagnostic", "dominance": "left_dominant",
           "dominance_left_probability": 0.814933}
    out = JsonPredictionResponse(predictions=[row]).model_dump()["predictions"][0]
    assert out["dominance_left_probability"] == 0.814933 and out["dominance"] == "left_dominant"
    assert out["series_time"] == "12:00:00" and isinstance(out["main_structure"], str)


def test_proxy_waits_as_long_as_the_gateway_for_a_complete_study():
    """A study is classified in one request; nginx's default 60 s read timeout failed a 37-video study."""
    conf = (ROOT / "nginx.conf").read_text(encoding="utf-8")
    for directive in ("proxy_read_timeout", "proxy_send_timeout"):
        values = [line.split()[1].rstrip(";") for line in conf.splitlines() if line.strip().startswith(directive)]
        assert values == ["300s"], directive  # api-pacs predictionClient waits 5 minutes


# --- 1.0.5: videos sharing one SeriesTime (exported/converted runs) ---------------------------------------------

from datetime import time as tm
from logic import chronology_times


def _video(i, series_time, acquisition=None, content=None):
    d = Dataset()
    d.SeriesInstanceUID, d.SOPInstanceUID, d.SeriesTime, d.Index = f"1.2.{i}", f"1.2.{i}.1", series_time, i
    if acquisition is not None:
        d.AcquisitionTime = acquisition
    if content is not None:
        d.ContentTime = content
    return d


def _labelled_service(monkeypatch, stents):
    """VasoVision stub: Left Coronary, contrast yes; stent present for the video indices in ``stents``."""
    service = CustomPredictionService()
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    monkeypatch.setattr(CustomPredictionService, "config", SimpleNamespace(
        forward_batch_size=1, preprocess_workers=1,
        head_structure=SimpleNamespace(main_structure=2, contrast_agent=1, stent_presence=1)), raising=False)
    monkeypatch.setattr(CustomPredictionService, "class_mapping", {
        "main_structure": {0: "Left Coronary", 1: "Right Coronary"},
        "contrast_agent": {0: "no", 1: "yes"},
        "stent_presence": {0: "absent", 1: "present"}}, raising=False)

    def model(batch):
        index = int(batch[0, 0, 0, 0, 0])
        return {"main_structure": torch.tensor([[2., 0.]]), "contrast_agent": torch.tensor([[1.]]),
                "stent_presence": torch.tensor([[1. if index in stents else -1.]])}

    monkeypatch.setattr(CustomPredictionService, "models", {"vaso_vision": model})
    monkeypatch.setattr(service, "_process_dicom", lambda d: torch.full((3, 2, 4, 4), float(d.Index)))
    return service


def _classify(service, dicoms, undetermined=False):
    predictions = service._run_inference(dicoms, [str(d.SeriesInstanceUID) for d in dicoms])
    return service._assign_procedure_status(predictions, undetermined_on_ambiguity=undetermined)


def test_chronology_uses_series_time_without_ties():
    entries = [(tm(16, 38, 24), tm(16, 38, 20), None), (tm(16, 40), None, None)]
    assert chronology_times(entries) == [(tm(16, 38, 24), "SeriesTime"), (tm(16, 40), "SeriesTime")]


def test_chronology_resolves_a_tie_with_distinct_acquisition_times_only_for_the_tied_group():
    entries = [(tm(16, 38, 24), tm(16, 39, 7, 114000), tm(1)), (tm(16, 38, 24), tm(16, 38, 20, 281000), tm(2)),
               (tm(17), tm(9), None)]
    assert chronology_times(entries) == [(tm(16, 39, 7, 114000), "AcquisitionTime"),
                                         (tm(16, 38, 20, 281000), "AcquisitionTime"), (tm(17), "SeriesTime")]


@pytest.mark.parametrize("acquisitions, expected", [
    ([None, tm(16, 39)], "ContentTime"),           # one AcquisitionTime missing
    ([tm(16, 39), tm(16, 39)], "ContentTime"),     # identical AcquisitionTimes
])
def test_chronology_falls_back_to_distinct_content_times_for_the_whole_group(acquisitions, expected):
    entries = [(tm(16, 38, 24), acquisitions[0], tm(16, 40)), (tm(16, 38, 24), acquisitions[1], tm(16, 41))]
    assert [source for _, source in chronology_times(entries)] == [expected] * 2


@pytest.mark.parametrize("acquisitions, contents", [
    ([None, None], [None, None]),
    ([tm(16, 39), tm(16, 39)], [tm(16, 40), tm(16, 40)]),
    ([tm(16, 39), None], [tm(16, 40), None]),
])
def test_chronology_keeps_the_shared_series_time_without_usable_distinct_times(acquisitions, contents):
    entries = [(tm(16, 38, 24), a, c) for a, c in zip(acquisitions, contents)]
    assert chronology_times(entries) == [(tm(16, 38, 24), "SeriesTime")] * 2


def test_exported_study_with_one_series_time_is_ordered_by_acquisition_time(monkeypatch):
    # Request order differs from acquisition order; video 2 (stent) was acquired second.
    service = _labelled_service(monkeypatch, stents={2})
    dicoms = [_video(1, "163824", "163939.615", "163940"), _video(2, "163824", "163907.114", "163908"),
              _video(3, "163824", "163820.281", "163821"), _video(4, "163824", "164012", "164013")]
    result = _classify(service, dicoms)
    assert [r["sop_instance_uid"] for r in result] == ["1.2.3.1", "1.2.2.1", "1.2.1.1", "1.2.4.1"]
    assert [r["status"] for r in result] == ["diagnostic", "PCI", "POST_PCI", "POST_PCI"]
    assert all(r["series_time"] == "16:38:24" and r["chronology_source"] == "AcquisitionTime" for r in result)
    assert [r["chronology_time"] for r in result] == ["16:38:20.281000", "16:39:07.114000", "16:39:39.615000", "16:40:12"]


@pytest.mark.parametrize("acquisitions", [[None, None], ["163907", "163907"], ["163907", "1639"]])
def test_tie_without_usable_acquisition_or_content_times_stays_ambiguous(monkeypatch, acquisitions):
    service = _labelled_service(monkeypatch, stents={2})
    dicoms = [_video(1, "163824", acquisitions[0]), _video(2, "163824", acquisitions[1])]
    with pytest.raises(ValueError, match="Ambiguous PCI timing"):
        _classify(service, dicoms)


def test_ambiguous_timing_returns_undetermined_labels_only_on_request(monkeypatch):
    service = _labelled_service(monkeypatch, stents={2})
    dicoms = [_video(1, "163824"), _video(2, "163824")]
    result = _classify(service, dicoms, undetermined=True)
    assert [r["status"] for r in result] == ["undetermined"] * 2
    assert all(r["status_reason"] == "ambiguous_acquisition_order" for r in result)
    assert [r["stent_presence"] for r in result] == ["absent", "present"]
    assert all(r["main_structure"] == "Left Coronary" and r["contrast_agent"] == "yes" for r in result)


def test_undetermined_request_does_not_change_an_orderable_study(monkeypatch):
    service = _labelled_service(monkeypatch, stents={2})
    dicoms = [_video(1, "163800"), _video(2, "163900"), _video(3, "164000")]
    assert [r["status"] for r in _classify(service, dicoms, undetermined=True)] == ["diagnostic", "PCI", "POST_PCI"]


def test_study_without_shared_series_time_keeps_its_previous_predictions(monkeypatch):
    """No new field and the same order/status whatever AcquisitionTime says."""
    service = _labelled_service(monkeypatch, stents={2})
    dicoms = [_video(1, "163800", "170000"), _video(2, "163900", "160000"), _video(3, "164000")]
    result = _classify(service, dicoms)
    assert [r["sop_instance_uid"] for r in result] == ["1.2.1.1", "1.2.2.1", "1.2.3.1"]
    assert [r["status"] for r in result] == ["diagnostic", "PCI", "POST_PCI"]
    assert all(set(r) == {"main_structure", "contrast_agent", "stent_presence", "series_time", "dicom_name",
                          "series_instance_uid", "sop_instance_uid", "status"} for r in result)


def _dicom_base64(d):
    import base64
    from io import BytesIO
    from pydicom.dataset import FileMetaDataset
    from pydicom.uid import ExplicitVRLittleEndian
    meta = FileMetaDataset()
    meta.MediaStorageSOPClassUID, meta.MediaStorageSOPInstanceUID = "1.2.840.10008.5.1.4.1.1.12.1", d.SOPInstanceUID
    meta.TransferSyntaxUID = ExplicitVRLittleEndian
    d.file_meta = meta
    del d.Index
    buffer = BytesIO()
    d.save_as(buffer, enforce_file_format=True)
    return base64.b64encode(buffer.getvalue()).decode()


@pytest.mark.parametrize("metadata, fails", [(None, True), ({}, True), ({"ambiguousTimingStatus": "other"}, True),
                                             ({"ambiguousTimingStatus": "undetermined"}, False)])
def test_json_request_opts_in_to_undetermined_status_through_additional_metadata(monkeypatch, metadata, fails):
    service = _labelled_service(monkeypatch, stents={2})
    videos = [_video(1, "163824"), _video(2, "163824")]
    monkeypatch.setattr(service, "_process_dicom", lambda d: torch.full((3, 2, 4, 4), float(d.SeriesInstanceUID[-1])))
    request = SimpleNamespace(seriesInstanceImages={i: {0: _dicom_base64(v)} for i, v in enumerate(videos)},
                              additionalMetadata=metadata)
    if fails:
        with pytest.raises(ValueError, match="Ambiguous PCI timing"):
            asyncio.run(service._handle_json_output(request))
    else:
        rows = asyncio.run(service._handle_json_output(request))["predictions"]
        assert [r["status"] for r in rows] == ["undetermined"] * 2


def test_declared_contract_advertises_chronology_and_undetermined_status():
    import json
    info = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    assert info["version"] == "1.0.6"
    assert info["preprocessingContract"]["chronologyTime"] == "series-time-ties-by-acquisition-time-v1"
    assert info["preprocessingContract"]["ambiguousTimingStatus"] == "undetermined-on-request"
