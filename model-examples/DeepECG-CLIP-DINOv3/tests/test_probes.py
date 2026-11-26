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
    return discover_probes(root, pf.ENCODER_CONFIG, pf.FROZEN_SHA256)


def test_heads_are_discovered_and_compute_deepecg_docker_probabilities(tmp_path):
    pf.standard_heads(tmp_path)
    probes = {p.key: p for p in _discover(tmp_path)}
    assert sorted(probes) == ["lvef_le_40", "patterns", "shd"]
    assert probes["patterns"].thresholds == {} and probes["shd"].thresholds == {"SHD": 0.5}
    x = torch.randn(2, 1536)
    t = probes["lvef_le_40"].tensors
    # DeepECG_Docker models/siglip2_wrapper.py _DeepECG_SigLIP2_Bank.probabilities
    expected = torch.sigmoid(((x - t["mean"]) / t["scale"]) @ t["weight"].T + t["bias"])
    torch.testing.assert_close(probes["lvef_le_40"](x), expected)
    t = probes["patterns"].tensors
    torch.testing.assert_close(probes["patterns"](x), torch.sigmoid(x @ t["weight"].T + t["bias"]))


def test_no_probe_directory_means_no_probes(tmp_path):
    assert _discover(tmp_path / "absent") == []


def _edit(folder: Path, name: str, change) -> None:
    document = json.loads((folder / name).read_text())
    change(document)
    (folder / name).write_text(json.dumps(document))


@pytest.mark.parametrize("name,change,message", [
    ("head.json", lambda d: d["encoder"].update(sha256="0" * 64), "pinned frozen encoder"),
    ("head.json", lambda d: d["classifier"].update(activation="softmax"), "classifier metadata"),
    ("head.json", lambda d: d["classifier"].update(input_dim=768), "classifier metadata"),
    ("head.json", lambda d: d.update(outputs=[]), "outputs"),
    ("thresholds.json", lambda d: d.update(thresholds={"SHD": 1.5}), "cutoff"),
    ("thresholds.json", lambda d: d.update(thresholds={"other": 0.5}), "cutoff"),
    ("config.json", lambda d: d["preprocessing"].update(resize=[224, 224]), "differs"),
])
def test_invalid_heads_are_refused(tmp_path, name, change, message):
    pf.write_head(tmp_path, "shd", ["SHD"], {"SHD": 0.5})
    _edit(tmp_path / "shd", name, change)
    with pytest.raises(RuntimeError, match=message):
        _discover(tmp_path)


def test_missing_file_or_wrong_tensors_are_refused(tmp_path):
    from safetensors.torch import save_file
    pf.write_head(tmp_path, "shd", ["SHD"], {"SHD": 0.5})
    save_file({"weight": torch.zeros(1, 768), "bias": torch.zeros(1), "mean": torch.zeros(1536),
               "scale": torch.ones(1536)}, str(tmp_path / "shd" / "classifier.safetensors"))
    with pytest.raises(RuntimeError, match="tensor shapes"):
        _discover(tmp_path)
    (tmp_path / "shd" / "thresholds.json").unlink()
    with pytest.raises(RuntimeError, match="missing thresholds.json"):
        _discover(tmp_path)
