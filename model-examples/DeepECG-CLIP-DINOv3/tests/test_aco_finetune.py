"""The fully fine-tuned ACO model loads from a DeepECG-PNG checkpoint and scores like the training class
(tiny random weights, no checkpoint download)."""
from __future__ import annotations

import sys
import types
from pathlib import Path

import pytest
import torch
from transformers import DINOv3ViTModel

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from models import aco_finetune, dinov3_bank  # noqa: E402

TINY = {**dinov3_bank.DINOV3_VITB16, "hidden_size": 32, "intermediate_size": 64, "num_hidden_layers": 2,
        "num_attention_heads": 2}


@pytest.fixture
def tiny(monkeypatch):
    monkeypatch.setattr(dinov3_bank, "DINOV3_VITB16", TINY)
    monkeypatch.setattr(dinov3_bank, "PROJECTION_DIM", 8)


def _training_state(layout: str) -> dict:
    """A DINOv3CLIPEncoder(num_classes=1) state dict as the unified runner saves it, blocks named `layout`."""
    torch.manual_seed(0)
    model = aco_finetune.FineTunedACO()
    state = {}
    for key, value in model.state_dict().items():
        for prefix in ("backbone.model.layer.", "backbone.layer."):
            if key.startswith(prefix):
                key = f"backbone.{layout}." + key[len(prefix):]
        state[f"module.{key}"] = value.clone()
    return state


def _save(path: Path, state: dict, monkeypatch, module: str = "utils.config.unified_config",
          name: str = "UnifiedECGConfig") -> None:
    """torch.save a checkpoint that pickles a class of `module`, then make that module unimportable."""
    fake = types.ModuleType(module)
    cls = type(name, (), {"__module__": module, "__qualname__": name})
    setattr(fake, name, cls)
    parts = module.split(".")
    for i in range(1, len(parts)):
        monkeypatch.setitem(sys.modules, ".".join(parts[:i]), types.ModuleType(".".join(parts[:i])))
    monkeypatch.setitem(sys.modules, module, fake)
    torch.save({"model_state_dict": state, "config": cls(), "epoch": 25}, path)
    for i in range(1, len(parts) + 1):
        monkeypatch.delitem(sys.modules, ".".join(parts[:i]))


@pytest.mark.parametrize("layout", ["model.layer", "layer"])
def test_checkpoint_loads_strictly_and_scores_like_the_training_class(tiny, tmp_path, monkeypatch, layout):
    path = tmp_path / "best_model.pth"
    state = _training_state(layout)
    _save(path, state, monkeypatch)
    model = aco_finetune.FineTunedACO()
    model.load_checkpoint(str(path))
    model.eval()
    x = torch.randn(2, 3, 32, 64)
    # DeepECG-PNG DINOv3CLIPEncoder.forward: backbone, concat[CLS, mean patches], Dropout + Linear
    with torch.no_grad():
        hidden = model.backbone(pixel_values=x).last_hidden_state
        logit = model.classifier(torch.cat([hidden[:, 0], hidden[:, 5:].mean(dim=1)], dim=-1)).reshape(-1)
    torch.testing.assert_close(model.probability(x), torch.sigmoid(logit))
    assert isinstance(model.backbone, DINOv3ViTModel)


def test_a_missing_tensor_fails(tiny, tmp_path, monkeypatch):
    state = _training_state("model.layer")
    del state["module.classifier.1.bias"]
    path = tmp_path / "best_model.pth"
    _save(path, state, monkeypatch)
    with pytest.raises(RuntimeError, match="missing"):
        aco_finetune.FineTunedACO().load_checkpoint(str(path))


def test_only_the_training_config_class_is_replaced(tiny, tmp_path, monkeypatch):
    path = tmp_path / "other.pth"
    _save(path, _training_state("model.layer"), monkeypatch, module="not_a_training_module", name="Payload")
    with pytest.raises(ModuleNotFoundError):
        aco_finetune.FineTunedACO().load_checkpoint(str(path))
