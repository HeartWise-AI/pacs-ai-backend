"""Source-level contract tests (no GPU, no weights)."""
import json
from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pytest

ROOT = Path(__file__).resolve().parents[1]


def test_manifest_is_research_only_and_pinned():
    info = json.loads((ROOT / "data/model_info.json").read_text())
    assert info["modelId"] == "EchoAlign"
    assert info["researchOnly"] is True
    assert "CC BY-NC 4.0" in info["license"]
    prov = info["provenance"]
    assert prov["modelRepository"] == "EchoAlign/EchoAlign"
    assert len(prov["modelRevision"]) == 40
    assert len(prov["weightsSha256"]) == 64


def test_facts_have_no_automatic_runs_and_state_research_use():
    facts = json.loads((ROOT / "data/model_facts.json").read_text())
    assert facts["automaticRunLabels"] == []
    assert "RESEARCH USE ONLY" in facts["en"]["Summary"]["Description"]


def test_downloader_pins_match_manifest():
    import download_model as dm
    prov = json.loads((ROOT / "data/model_info.json").read_text())["provenance"]
    assert (dm.EXPECTED_REPOSITORY, dm.EXPECTED_REVISION, dm.EXPECTED_WEIGHTS_PATH) == (
        prov["modelRepository"], prov["modelRevision"], prov["weightsPath"])
    assert all(len(v) == 64 for v in dm.COMPANION_FILES.values())
    with pytest.raises(ValueError):
        dm.download_model("EchoAlign/EchoAlign", "main", "EchoAlign.pt", prov["weightsSha256"])


@pytest.mark.skipif(not (ROOT / ".gitignore").exists() or (ROOT / "models" / "EchoAlign.pt").exists(),
                    reason="inside the built image the downloaded weights are expected")
def test_no_weights_in_source():
    assert not list(ROOT.rglob("*.pt")) and not list(ROOT.rglob("*.npz"))


def _ds(**kw):
    base = dict(Modality="US", Rows=10, NumberOfFrames=30, SeriesDescription="", ProtocolName="", StudyDescription="")
    base.update(kw)
    return SimpleNamespace(**base)


def test_instance_selection_matches_panecho_by_default():
    from logic import select_reason
    assert select_reason(_ds(), "multiframe") is None
    assert select_reason(_ds(NumberOfFrames=1), "multiframe") == "single_frame"
    assert select_reason(_ds(NumberOfFrames=1), "all") is None
    assert select_reason(_ds(SeriesDescription="ETO 3D"), "all") == "tee"
    assert select_reason(_ds(Modality="SR"), "all") == "non_image"
    assert select_reason(_ds(StudyDescription="ETO PERIOPERATOIRE"), "multiframe") == "tee"
    assert select_reason(_ds(StudyDescription="Echo trans-oesophagienne"), "multiframe") == "tee"
    assert select_reason(_ds(TransducerData=["X7-2t", "", ""]), "multiframe") == "tee"
    assert select_reason(_ds(TransducerData="6VT-D"), "multiframe") == "tee"
    assert select_reason(_ds(StudyDescription="ECHO CARDIAQUE AVEC DOPPLER", TransducerData="M5Sc"), "multiframe") is None


def test_loader_converts_high_bit_depth_and_monochrome1(tmp_path):
    import numpy as np
    import pydicom
    from pydicom.dataset import FileDataset, FileMetaDataset
    from pydicom.uid import ExplicitVRLittleEndian, generate_uid
    from models.echoalign.loader import read_frames

    def write(arr, photometric, bits):
        meta = FileMetaDataset(); meta.TransferSyntaxUID = ExplicitVRLittleEndian
        meta.MediaStorageSOPClassUID = "1.2.840.10008.5.1.4.1.1.3.1"; meta.MediaStorageSOPInstanceUID = generate_uid()
        ds = FileDataset(str(tmp_path / "x.dcm"), {}, file_meta=meta, preamble=b"\0" * 128)
        ds.Modality = "US"; ds.Rows, ds.Columns = arr.shape[1:]; ds.NumberOfFrames = arr.shape[0]
        ds.SamplesPerPixel = 1; ds.PhotometricInterpretation = photometric
        ds.BitsAllocated = bits; ds.BitsStored = bits; ds.HighBit = bits - 1; ds.PixelRepresentation = 0
        ds.PixelData = arr.tobytes(); path = tmp_path / f"{photometric}{bits}.dcm"; ds.save_as(path, enforce_file_format=True)
        return path

    ramp = np.tile(np.linspace(0, 4000, 8, dtype=np.uint16), (2, 8, 1))
    frames = read_frames(write(ramp, "MONOCHROME2", 16), n_frames=1, target_fps=None)
    row = np.asarray(frames[0])[0, :, 0]
    assert row[0] < row[-1] and row[-1] >= 250, "16-bit values are windowed, not wrapped"
    eight = np.tile(np.arange(0, 256, 32, dtype=np.uint8), (2, 8, 1))
    inv = np.asarray(read_frames(write(eight, "MONOCHROME1", 8), n_frames=1, target_fps=None)[0])[0, :, 0]
    assert inv[0] == 255 and inv[-1] == 255 - 224
    same = np.asarray(read_frames(write(eight, "MONOCHROME2", 8), n_frames=1, target_fps=None)[0])[0, :, 0]
    assert list(same) == list(range(0, 256, 32)), "8-bit input is unchanged"


def test_heads_follow_the_model_card_recipe():
    from logic import apply_head
    d = 4
    heads = {"c/mean": np.zeros(d), "c/scale": np.ones(d), "c/coef": np.array([1.0, 0, 0, 0]), "c/intercept": np.array(0.0),
             "o/mean": np.zeros(d), "o/scale": np.ones(d), "o/coef": np.eye(4), "o/intercept": np.zeros(4),
             "r/mean": np.zeros(d), "r/scale": np.ones(d), "r/coef": np.array([0, 2.0, 0, 0]), "r/intercept": np.array(1.0)}
    meta = {"c": {"mode": "classification", "threshold": 0.4}, "o": {"mode": "ordinal", "n_classes": 4, "cuts": [0.5, 1.5, 2.5]},
            "r": {"mode": "regression"}}
    grades = {"aortic_stenosis": {"grades": ["normal", "mild", "moderate", "severe"]}}
    x = np.array([0.0, 1.0, 0.0, 9.0])
    c = apply_head(x, "c", meta, heads, grades)
    assert c["probability"] == 0.5 and c["called"] is True
    o = apply_head(x, "o", meta, heads, grades)
    assert o["grade"] == "3" and o["gradeIndex"] == 3  # no grade names for an unknown head
    assert apply_head(x, "r", meta, heads, grades)["value"] == 3.0


def test_html_has_research_banner():
    from logic import RESEARCH_BANNER, CustomPredictionService
    assert RESEARCH_BANNER == "Research use only — EchoAlign (CC BY-NC 4.0), not for clinical decisions"
    head = {"type": "regression", "value": 2.5, "unit": "m/s"}
    result = {"keyHeads": {"transaortic_peak_velocity": head}, "clipsUsed": 3, "instanceSelection": "multiframe",
              "summary": {"aorticStenosis": {"grade": "mild", "severeProbability": 0.01, "moderateOrSevereProbability": 0.2,
                                             "avPeakVelocity": 2.5, "meanGradient": 15.0}, "ejectionFraction": 60.0}}
    page = CustomPredictionService.render_html(result)
    assert "Research use only — EchoAlign (CC BY-NC 4.0), not for clinical decisions" in page
    assert "arXiv:2611.27517" in page
