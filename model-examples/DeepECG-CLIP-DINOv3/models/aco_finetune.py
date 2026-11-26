"""The fully fine-tuned ACO model, served next to the frozen-probe heads (decision of 2026-11-26).

DeepECG-PNG ``DINOv3CLIPEncoder`` fine-tuned on the ACS cohort from the same DINOv3-SigLIP2
encoder (zo59vdxk, epoch 30): top 2 blocks + final norm + ``classifier = Dropout + Linear(1536, 1)``
trained (``training_mode: partial``), fp32. Held-out MHI test AUROC 0.92 [0.89, 0.93]; threshold
0.05062838 (augmented-validation Youden). It runs its own backbone (the encoder of
models/dinov3_bank.py with a one-logit head) on the same preprocessed page as the probes, in fp32
as it was trained and evaluated.

The checkpoint (heartwise/deepecg_clip_dinov3_acs, acs_ft20_dinov3_siglip2_epoch030/aco/best_model.pth)
pickles the training run's config object next to the weights; only that class is replaced by an
inert stand-in when loading, everything else loads as usual. Its bytes are verified against their
SHA-256 at build (download_model.py) before this runs.
"""
from __future__ import annotations

import pickle
import types

import torch
import torch.nn as nn

from models.dinov3_bank import DeepECG_DINOv3_Encoder

STATE_DICT_KEY = "model_state_dict"


class _InertObject:
    def __init__(self, *args, **kwargs):
        pass

    def __setstate__(self, state):
        self.state = state


class _CheckpointUnpickler(pickle.Unpickler):
    def find_class(self, module: str, name: str):
        if module == "utils.config.unified_config":
            return _InertObject
        return super().find_class(module, name)


_PICKLE_MODULE = types.SimpleNamespace(Unpickler=_CheckpointUnpickler, load=pickle.load,
                                       __name__="deepecg_checkpoint_pickle")


def load_checkpoint_state(path: str) -> dict[str, torch.Tensor]:
    """``model_state_dict`` of a DeepECG-PNG unified-runner checkpoint, ``module.`` stripped."""
    checkpoint = torch.load(path, map_location="cpu", weights_only=False, pickle_module=_PICKLE_MODULE)
    if not isinstance(checkpoint, dict) or not isinstance(checkpoint.get(STATE_DICT_KEY), dict):
        raise RuntimeError(f"{path}: no '{STATE_DICT_KEY}'")
    return {key.removeprefix("module."): value for key, value in checkpoint[STATE_DICT_KEY].items()}


class FineTunedACO(DeepECG_DINOv3_Encoder):
    """DINOv3 ViT-B/16 + Dropout + Linear(1536, 1): the ACO fine-tune, one logit per page."""

    def __init__(self, dropout: float = 0.1):
        super().__init__()
        self.classifier = nn.Sequential(nn.Dropout(p=dropout), nn.Linear(self.feature_dim, 1))

    def load_checkpoint(self, path: str) -> None:
        """Strict load; the block names are mapped to this transformers install as in the bank."""
        own = set(self.state_dict())
        mapped = {}
        for key, value in load_checkpoint_state(path).items():
            if key not in own and key.startswith("backbone.model.layer."):
                key = "backbone.layer." + key[len("backbone.model.layer."):]
            elif key not in own and key.startswith("backbone.layer."):
                key = "backbone.model.layer." + key[len("backbone.layer."):]
            mapped[key] = value
        missing, unexpected = self.load_state_dict(mapped, strict=False)
        if missing or unexpected:
            raise RuntimeError(f"{path}: missing {sorted(missing)[:5]}, unexpected {sorted(unexpected)[:5]}")

    @torch.no_grad()
    def probability(self, pages: torch.Tensor) -> torch.Tensor:
        """(B, 3, H, W) preprocessed pages -> (B,) ACO probabilities, fp32."""
        device = next(self.parameters()).device
        return torch.sigmoid(self.classifier(self(pages.to(device, torch.float32)))).reshape(-1).float().cpu()
