"""DINOv3 ViT-B/16 ECG classifiers that share their frozen layers.

Each released DeepECG-CLIP DINOv3 head is a whole ``DINOv3CLIPEncoder`` checkpoint
(HeartWise-AI/DeepECG-PNG ``models/dinov3_clip.py``): the DINOv3 backbone, a final
LayerNorm and ``classifier = Dropout + Linear(1536, 1)`` on concat[CLS, mean(patch tokens)].
The heads were fine-tuned with ``training_mode: partial, unfreeze_top_n: 2``, so the patch
embedding and layers 0-9 are the frozen pretraining weights and are bit-identical in every
head; only layers 10-11, the final norm and the classifier differ.

``SharedTrunkClassifiers`` runs the frozen part once per image and then each head's own
top layers. Optionally it also runs the frozen pretraining encoder's own top layers
(``frozen``: layers 10-11 + norm of the DeepECG-CLIP image tower, base/epoch_030.pt) and
returns its pooled features, which the frozen linear probes read (models/probes.py).

Loading checks that every head's frozen tensors are bit-identical to the first
head's, so a checkpoint trained with another policy cannot be served through the shared
trunk by mistake. The output of each head equals the training class's
``DINOv3CLIPEncoder.forward`` (tests/test_model_heads.py; against the released checkpoints and the training code, see README.md).
"""
from __future__ import annotations

import copy
import pickle
import types
from typing import Any

import torch
import torch.nn as nn
from transformers import DINOv3ViTConfig, DINOv3ViTModel

STATE_DICT_KEY = "model_state_dict"
BACKBONE_PREFIX = "backbone."
CLASSIFIER_PREFIX = "classifier."


class _InertObject:
    """Stand-in for the training config object pickled in the checkpoints. Never called."""

    def __init__(self, *args, **kwargs):
        pass

    def __setstate__(self, state):
        self.state = state


class _CheckpointUnpickler(pickle.Unpickler):
    """Loads the training checkpoints without the DeepECG-PNG code: they pickle the run's
    ``utils.config.unified_config.UnifiedECGConfig`` next to the weights. Only that module is
    replaced; tensors, numpy arrays and containers load as usual. The checkpoint bytes are
    verified against their SHA-256 at build time (download_model.py) before this runs."""

    def find_class(self, module: str, name: str):
        if module == "utils.config.unified_config":
            return _InertObject
        return super().find_class(module, name)


_PICKLE_MODULE = types.SimpleNamespace(
    Unpickler=_CheckpointUnpickler, load=pickle.load, __name__="deepecg_checkpoint_pickle"
)


def load_state_dict(path: str, key: str = STATE_DICT_KEY) -> dict[str, torch.Tensor]:
    """The model weights of one released checkpoint (``key``, ``module.`` stripped): the
    fine-tunes keep them under ``model_state_dict``, the contrastive pretraining checkpoint
    keeps the image tower under ``image_encoder``."""
    checkpoint = torch.load(
        path, map_location="cpu", weights_only=False, pickle_module=_PICKLE_MODULE, mmap=True
    )
    state = checkpoint[key] if isinstance(checkpoint, dict) and key in checkpoint else checkpoint
    if not isinstance(state, dict) or not state:
        raise ValueError(f"{path} holds no model weights")
    return {key.removeprefix("module."): value for key, value in state.items()}


def backbone_config(settings: dict[str, Any]) -> DINOv3ViTConfig:
    """The DINOv3 ViT-B/16 architecture, built locally: the gated Hugging Face repository
    facebook/dinov3-vitb16-pretrain-lvd1689m is never contacted."""
    config = DINOv3ViTConfig(**settings)
    config._attn_implementation = "sdpa"   # as DINOv3CLIPEncoder.from_pretrained(attn_implementation="sdpa")
    return config


class _HeadBranch(nn.Module):
    """One head's own top layers, final LayerNorm and classifier."""

    def __init__(self, layers: nn.ModuleList, norm: nn.LayerNorm, feature_dim: int, dropout: float,
                 with_classifier: bool = True):
        super().__init__()
        self.layers = layers
        self.norm = norm
        self.classifier = (nn.Sequential(nn.Dropout(p=dropout), nn.Linear(feature_dim, 1))
                           if with_classifier else None)


class SharedTrunkClassifiers(nn.Module):
    """The frozen DINOv3 layers, run once, followed by every head's own top layers and, when
    ``with_frozen_encoder``, by the pretraining encoder's top layers for the linear probes."""

    def __init__(self, settings: dict[str, Any], heads: list[str], shared_layers: int, dropout: float,
                 with_frozen_encoder: bool = False):
        super().__init__()
        config = backbone_config(settings)
        if not 0 < shared_layers < config.num_hidden_layers:
            raise ValueError(f"shared_layers must be within 1..{config.num_hidden_layers - 1}")
        backbone = DINOv3ViTModel(config)
        self.patch_size = int(config.patch_size)
        self.n_prefix = 1 + int(config.num_register_tokens)
        self.shared_layers = int(shared_layers)
        self.heads = list(heads)
        feature_dim = 2 * int(config.hidden_size)

        self.embeddings = backbone.embeddings
        self.rope_embeddings = backbone.rope_embeddings
        self.trunk = nn.ModuleList(backbone.model.layer[: self.shared_layers])
        top = backbone.model.layer[self.shared_layers:]
        self.branches = nn.ModuleDict({
            head: _HeadBranch(copy.deepcopy(top), copy.deepcopy(backbone.norm), feature_dim, dropout)
            for head in self.heads
        })
        self.frozen = (_HeadBranch(copy.deepcopy(top), copy.deepcopy(backbone.norm), feature_dim, dropout,
                                   with_classifier=False)
                       if with_frozen_encoder else None)

    # -- weights -----------------------------------------------------------

    def _shared_key(self, key: str) -> bool:
        if not key.startswith(BACKBONE_PREFIX):
            return False
        rest = key[len(BACKBONE_PREFIX):]
        if rest.startswith(("embeddings.", "rope_embeddings.")):
            return True
        if rest.startswith("model.layer."):
            return int(rest.split(".")[2]) < self.shared_layers
        return False

    def _trunk_name(self, key: str) -> str:
        rest = key[len(BACKBONE_PREFIX):]
        if rest.startswith("model.layer."):
            return "trunk." + rest[len("model.layer."):]
        return rest

    def _branch_name(self, head: str, key: str) -> str:
        prefix = "frozen" if head == "frozen" else f"branches.{head}"
        if key.startswith(CLASSIFIER_PREFIX):
            return f"{prefix}.{key}"
        rest = key[len(BACKBONE_PREFIX):]
        if rest.startswith("model.layer."):
            index = int(rest.split(".")[2]) - self.shared_layers
            tail = rest.split(".", 3)[3]
            return f"{prefix}.layers.{index}.{tail}"
        if rest.startswith("norm."):
            return f"{prefix}.{rest}"
        raise KeyError(f"Unexpected checkpoint tensor: {key}")

    def load_heads(self, states: dict[str, dict[str, torch.Tensor]],
                   frozen_state: dict[str, torch.Tensor] | None = None) -> None:
        """Strictly load every head's checkpoint (and the frozen encoder's backbone). The frozen
        tensors must be bit-identical across all of them; every model tensor must be loaded and
        every checkpoint tensor used (the frozen encoder's 512-d CLIP projection is not used)."""
        if list(states) != self.heads:
            raise ValueError(f"Expected checkpoints for {self.heads}, received {list(states)}")
        if (frozen_state is None) != (self.frozen is None):
            raise ValueError("The frozen encoder checkpoint must be given exactly when the model has that branch")
        states = dict(states)
        if frozen_state is not None:
            states["frozen"] = {k: v for k, v in frozen_state.items() if not k.startswith(CLASSIFIER_PREFIX)}
        reference = states[self.heads[0]]
        merged: dict[str, torch.Tensor] = {}
        for head, state in states.items():
            shared = {key for key in state if self._shared_key(key)}
            if shared != {key for key in reference if self._shared_key(key)}:
                raise ValueError(f"{head}: its frozen layers do not match the other heads' tensors")
            for key, value in state.items():
                if self._shared_key(key):
                    if not torch.equal(value, reference[key]):
                        raise ValueError(
                            f"{head}: {key} differs from {self.heads[0]}; the heads do not share "
                            f"their first {self.shared_layers} layers and cannot run on one trunk"
                        )
                    merged.setdefault(self._trunk_name(key), value)
                else:
                    merged[self._branch_name(head, key)] = value
        self.load_state_dict(merged, strict=True)

    # -- forward -------------------------------------------------------------

    def _pooled(self, branch: _HeadBranch, hidden: torch.Tensor, position_embeddings) -> torch.Tensor:
        for layer in branch.layers:
            hidden = layer(hidden, position_embeddings=position_embeddings)
        hidden = branch.norm(hidden)
        return torch.cat([hidden[:, 0], hidden[:, self.n_prefix:].mean(dim=1)], dim=-1)

    def forward(self, pixel_values: torch.Tensor,
                frozen_features: bool = False) -> tuple[dict[str, torch.Tensor], torch.Tensor | None]:
        """``pixel_values`` [B, 3, H, W] (normalised) -> ({head: logit [B, 1]}, frozen encoder
        features [B, 1536] when ``frozen_features`` else None)."""
        height, width = int(pixel_values.shape[-2]), int(pixel_values.shape[-1])
        if height % self.patch_size or width % self.patch_size:
            raise ValueError(f"input {height}x{width} is not a multiple of the {self.patch_size}-pixel patch")
        pixel_values = pixel_values.to(self.embeddings.patch_embeddings.weight.dtype)
        hidden = self.embeddings(pixel_values)
        position_embeddings = self.rope_embeddings(pixel_values)
        for layer in self.trunk:
            hidden = layer(hidden, position_embeddings=position_embeddings)

        logits = {head: branch.classifier(self._pooled(branch, hidden, position_embeddings))
                  for head, branch in self.branches.items()}
        features = None
        if frozen_features:
            if self.frozen is None:
                raise ValueError("This model was built without the frozen encoder")
            features = self._pooled(self.frozen, hidden, position_embeddings)
        return logits, features
