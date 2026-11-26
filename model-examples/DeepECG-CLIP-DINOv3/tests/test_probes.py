"""Frozen linear-probe heads load from DeepECG_Docker head folders and compute its probabilities."""
from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest
import torch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import probe_fixtures as pf  # noqa: E402
from models.probes import discover_probes  # noqa: E402


def _discover(root):
    return discover_probes(root, pf.FROZEN_SHA256)


def test_heads_are_discovered_and_compute_deepecg_docker_probabilities(tmp_path):
    pf.standard_heads(tmp_path)
    probes = {p.key: p for p in _discover(tmp_path)}
    assert sorted(probes) == ["ecg77_clean", "lvef_le40_clean", "shd_clean"]
    assert probes["ecg77_clean"].thresholds == {} and probes["shd_clean"].thresholds == {"SHD": 0.5}
    assert probes["shd_clean"].name == "heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030/probes/shd_clean"
    x = torch.randn(2, 1536)
    t = probes["lvef_le40_clean"].head["tensors"]
    # DeepECG_Docker models/dinov3_bank.py head_probabilities
    expected = torch.sigmoid(((x - t["mean"]) / t["scale"]) @ t["weight"].T + t["bias"])
    torch.testing.assert_close(probes["lvef_le40_clean"](x), expected)
    t = probes["ecg77_clean"].head["tensors"]
    torch.testing.assert_close(probes["ecg77_clean"](x), torch.sigmoid(x @ t["weight"].T + t["bias"]))
    assert probes["ecg77_clean"](x).shape == (2, 77)


def test_a_three_class_head_is_a_softmax(tmp_path):
    outputs = ["rest", "probable HCM", "confirmed HCM"]
    pf.write_head(tmp_path, "hcm_3class_clean", outputs, {}, activation="softmax")
    (probe,) = _discover(tmp_path)
    assert probe.activation == "softmax" and probe.outputs == outputs
    torch.testing.assert_close(probe(torch.randn(4, 1536)).sum(dim=1), torch.ones(4))


def test_no_probe_directory_means_no_probes(tmp_path):
    assert _discover(tmp_path / "absent") == []


def _edit(folder: Path, name: str, change) -> None:
    document = json.loads((folder / name).read_text())
    change(document)
    (folder / name).write_text(json.dumps(document))


@pytest.mark.parametrize("name,change,message", [
    ("head.json", lambda d: d["encoder"].update(sha256="0" * 64), "does not match the loaded encoder"),
    ("head.json", lambda d: d["classifier"].update(activation="softmax"), "classifier metadata"),
    ("head.json", lambda d: d["classifier"].update(input_dim=768), "classifier metadata"),
    ("head.json", lambda d: d.update(outputs=[]), "outputs"),
    ("head.json", lambda d: d.update(outputs=["LVEF<50"]), "do not match"),
    ("head.json", lambda d: d.update(name="heartwise/other/probes/shd_clean"), "head name"),
    ("thresholds.json", lambda d: d.update(thresholds={"SHD": 1.5}), "cutoff"),
    ("thresholds.json", lambda d: d.update(thresholds={"other": 0.5}), "cutoff"),
    ("config.json", lambda d: d["preprocessing"].update(resize=[224, 224]), "differs"),
])
def test_invalid_heads_are_refused(tmp_path, name, change, message):
    pf.write_head(tmp_path, "shd_clean", ["SHD"], {"SHD": 0.5})
    _edit(tmp_path / "shd_clean", name, change)
    with pytest.raises(RuntimeError, match=message):
        _discover(tmp_path)


def test_folder_names_and_the_probe_config_are_checked(tmp_path):
    pf.write_head(tmp_path, "shd", ["SHD"], {"SHD": 0.5})
    with pytest.raises(RuntimeError, match="probe folders are named"):
        _discover(tmp_path)
    other = tmp_path / "other"
    pf.write_head(other, "shd_clean", ["SHD"], {"SHD": 0.5})
    _edit(other, "config.json", lambda d: d["checkpoint"].update(sha256="0" * 64))
    with pytest.raises(RuntimeError, match="encoder checkpoint does not match"):
        _discover(other)
    with pytest.raises(RuntimeError, match="not the encoder of the probe heads"):
        discover_probes(tmp_path, "0" * 64)       # models/config.json pins another frozen encoder


def test_missing_file_or_wrong_tensors_are_refused(tmp_path):
    from safetensors.torch import save_file
    pf.write_head(tmp_path, "shd_clean", ["SHD"], {"SHD": 0.5})
    save_file({"weight": torch.zeros(1, 768), "bias": torch.zeros(1), "mean": torch.zeros(1536),
               "scale": torch.ones(1536)}, str(tmp_path / "shd_clean" / "classifier.safetensors"))
    with pytest.raises(RuntimeError, match="tensor shapes"):
        _discover(tmp_path)
    (tmp_path / "shd_clean" / "thresholds.json").unlink()
    with pytest.raises(RuntimeError, match="missing thresholds.json"):
        _discover(tmp_path)
