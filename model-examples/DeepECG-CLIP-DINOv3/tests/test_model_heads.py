"""The shared-trunk model equals one DINOv3CLIPEncoder per head (tiny random weights, no checkpoint)."""
from __future__ import annotations

import json
import pickle
import sys
import types
from pathlib import Path

import pytest
import torch
import torch.nn as nn
from transformers import DINOv3ViTModel

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from models.dinov3_heads import SharedTrunkClassifiers, backbone_config, load_state_dict  # noqa: E402

TINY = {**json.loads((ROOT / "models/encoder_config.json").read_text())["vision_config"],
        "hidden_size": 32, "intermediate_size": 64, "num_hidden_layers": 4, "num_attention_heads": 2}
HEADS = ["aco", "acco_vs_aico"]


class TrainingClassifier(nn.Module):
    """DeepECG-PNG DINOv3CLIPEncoder.forward with num_classes=1: backbone, final norm,
    concat[CLS, mean(patch tokens)] (4 registers dropped), Dropout + Linear."""

    def __init__(self):
        super().__init__()
        self.backbone = DINOv3ViTModel(backbone_config(TINY))
        self.classifier = nn.Sequential(nn.Dropout(0.1), nn.Linear(2 * TINY["hidden_size"], 1))

    def forward(self, x):
        hidden = self.backbone(pixel_values=x).last_hidden_state
        return self.classifier(torch.cat([hidden[:, 0], hidden[:, 5:].mean(dim=1)], dim=-1))


def _partial_finetunes(shared_layers: int) -> list[TrainingClassifier]:
    """Two heads that share the frozen layers, as a partial fine-tune produces."""
    torch.manual_seed(0)
    first = TrainingClassifier()
    second = TrainingClassifier()
    frozen = {k: v for k, v in first.state_dict().items()
              if k.startswith(("backbone.embeddings.", "backbone.rope_embeddings."))
              or (k.startswith("backbone.model.layer.") and int(k.split(".")[3]) < shared_layers)}
    second.load_state_dict(frozen, strict=False)
    return [first.eval(), second.eval()]


def test_shared_trunk_outputs_equal_the_training_class():
    heads = _partial_finetunes(shared_layers=3)
    model = SharedTrunkClassifiers(TINY, HEADS, shared_layers=3, dropout=0.1).eval()
    model.load_heads({h: m.state_dict() for h, m in zip(HEADS, heads)})
    x = torch.randn(2, 3, 32, 64)
    with torch.no_grad():
        logits, _ = model(x)
        for head, reference in zip(HEADS, heads):
            torch.testing.assert_close(logits[head], reference(x), rtol=0, atol=1e-6)


def test_heads_that_do_not_share_their_frozen_layers_are_refused():
    heads = _partial_finetunes(shared_layers=2)
    model = SharedTrunkClassifiers(TINY, HEADS, shared_layers=3, dropout=0.1)
    with pytest.raises(ValueError, match="do not share"):
        model.load_heads({h: m.state_dict() for h, m in zip(HEADS, heads)})


def test_a_missing_tensor_fails_the_strict_load():
    heads = _partial_finetunes(shared_layers=3)
    states = {h: dict(m.state_dict()) for h, m in zip(HEADS, heads)}
    del states["acco_vs_aico"]["classifier.1.bias"]
    model = SharedTrunkClassifiers(TINY, HEADS, shared_layers=3, dropout=0.1)
    with pytest.raises(RuntimeError, match="Missing key"):
        model.load_heads(states)


def test_released_checkpoint_layout_loads_without_the_training_code(tmp_path, monkeypatch):
    """The released .pth files pickle utils.config.unified_config.UnifiedECGConfig next to the weights."""
    module = types.ModuleType("utils.config.unified_config")

    class UnifiedECGConfig:
        def __init__(self):
            self.model = {"name": "dinov3_vitb16_clip"}

    UnifiedECGConfig.__module__, UnifiedECGConfig.__qualname__ = module.__name__, "UnifiedECGConfig"
    module.UnifiedECGConfig = UnifiedECGConfig
    for name in ("utils.config", "utils.config.unified_config"):
        monkeypatch.setitem(sys.modules, name, module if name.endswith("unified_config") else types.ModuleType(name))
    weights = {"module.classifier.1.weight": torch.ones(1, 3)}
    path = tmp_path / "best_model.pth"
    torch.save({"model_state_dict": weights, "config": UnifiedECGConfig(), "epoch": 25}, path)
    for name in ("utils.config", "utils.config.unified_config"):
        monkeypatch.delitem(sys.modules, name)

    state = load_state_dict(str(path))
    assert list(state) == ["classifier.1.weight"]
    assert torch.equal(state["classifier.1.weight"], torch.ones(1, 3))


def test_only_the_training_config_class_is_replaced(tmp_path, monkeypatch):
    module = types.ModuleType("not_a_training_module")

    class Payload:
        pass

    Payload.__module__, Payload.__qualname__ = module.__name__, "Payload"
    module.Payload = Payload
    monkeypatch.setitem(sys.modules, module.__name__, module)
    path = tmp_path / "other.pth"
    torch.save({"model_state_dict": {"w": torch.ones(1)}, "extra": Payload()}, path)
    monkeypatch.delitem(sys.modules, module.__name__)

    with pytest.raises(ModuleNotFoundError):
        load_state_dict(str(path))


def test_frozen_encoder_features_equal_the_pretraining_tower():
    heads = _partial_finetunes(shared_layers=3)
    torch.manual_seed(1)
    base = TrainingClassifier()
    base.load_state_dict({k: v for k, v in heads[0].state_dict().items()
                          if k.startswith(("backbone.embeddings.", "backbone.rope_embeddings."))
                          or (k.startswith("backbone.model.layer.") and int(k.split(".")[3]) < 3)}, strict=False)
    base_state = dict(base.eval().state_dict())
    base_state["classifier.1.weight"] = torch.zeros(7, 2 * TINY["hidden_size"])   # the 512-d CLIP projection
    model = SharedTrunkClassifiers(TINY, HEADS, shared_layers=3, dropout=0.1, with_frozen_encoder=True).eval()
    model.load_heads({h: m.state_dict() for h, m in zip(HEADS, heads)}, base_state)
    x = torch.randn(1, 3, 32, 64)
    with torch.no_grad():
        logits, features = model(x, frozen_features=True)
        hidden = base.backbone(pixel_values=x).last_hidden_state
        expected = torch.cat([hidden[:, 0], hidden[:, 5:].mean(dim=1)], dim=-1)
        torch.testing.assert_close(features, expected, rtol=0, atol=1e-6)
        torch.testing.assert_close(logits["aco"], heads[0](x), rtol=0, atol=1e-6)
