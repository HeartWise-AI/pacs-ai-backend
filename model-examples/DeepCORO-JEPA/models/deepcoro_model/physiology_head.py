"""Attachable continuous FFR/iFR and abnormality head for one shared study model.

The head consumes image-derived study vectors. Measured vessel position is used
only in label provenance, never as an input. Modality selects the observed loss;
both modality outputs are produced in the same forward pass.
"""
from __future__ import annotations

from typing import Sequence

import torch
from torch import nn
from torch.nn import functional as F


class PhysiologyHead(nn.Module):
    def __init__(
        self,
        feature_dim: int,
        geometry_dim: int = 0,
        hidden_dim: int = 256,
        dropout: float = 0.35,
        group_indices: Sequence[int] = (5, 6, 7, 8, 9, 10),
    ):
        """feature_dim is the total flattened dimension after selecting groups.

        For canonical (B,18,1024) study features use feature_dim=6144.
        A preflattened (B,feature_dim) tensor is accepted as well.
        """
        super().__init__()
        self.feature_dim = feature_dim
        self.geometry_dim = geometry_dim
        self.group_indices = tuple(group_indices)
        if geometry_dim:
            # These buffers are fitted only on training studies by the caller.
            # geometry_dim=0 retains the existing state-dict contract.
            self.register_buffer("geometry_mean", torch.zeros(geometry_dim))
            self.register_buffer("geometry_scale", torch.ones(geometry_dim))
        self.net = nn.Sequential(
            nn.LayerNorm(feature_dim + geometry_dim),
            nn.Linear(feature_dim + geometry_dim, hidden_dim),
            nn.GELU(),
            nn.Dropout(dropout),
            nn.Linear(hidden_dim, 4),
        )

    @torch.no_grad()
    def set_geometry_normalization(self, training_geometry):
        if not self.geometry_dim:
            raise ValueError("head has no geometry inputs")
        x = torch.as_tensor(training_geometry, dtype=torch.float32, device=self.geometry_mean.device)
        if x.ndim != 2 or x.shape[1] != self.geometry_dim:
            raise ValueError(f"expected training geometry (N,{self.geometry_dim})")
        known = torch.isfinite(x)
        count = known.sum(0).clamp_min(1)
        mean = torch.where(known, x, 0).sum(0) / count
        var = torch.where(known, (x - mean).square(), 0).sum(0) / count
        scale = torch.where(var > 1e-8, var.sqrt(), torch.ones_like(var))
        self.geometry_mean.copy_(mean)
        self.geometry_scale.copy_(scale)

    def forward(self, study_features, geometry=None, modality=None):
        if study_features.ndim == 3:
            study_features = study_features[:, self.group_indices].flatten(1)
        if study_features.shape[-1] != self.feature_dim:
            raise ValueError(f"expected {self.feature_dim} image features, got {study_features.shape[-1]}")
        if self.geometry_dim:
            if geometry is None or geometry.shape[-1] != self.geometry_dim:
                raise ValueError(f"geometry must have {self.geometry_dim} features")
            geometry = geometry.float()
            geometry = torch.where(torch.isfinite(geometry), geometry, self.geometry_mean)
            geometry = (geometry - self.geometry_mean) / self.geometry_scale
            geometry = geometry.clamp(-10, 10).to(study_features.dtype)
            study_features = torch.cat((study_features, geometry), -1)
        raw = self.net(study_features).float()
        return {
            "ffr": raw[..., 0].sigmoid() * 1.05,
            "ifr": raw[..., 1].sigmoid() * 1.05,
            "ffr_logit": raw[..., 2],
            "ifr_logit": raw[..., 3],
        }


def physiology_loss(outputs, value, is_ffr, abnormal=None, regression_weight=4.0):
    """One observed modality per row; repeated study rows stay in one patient fold.

    Missing measurements do not supervise the other modality's head. Regression
    and threshold classification share the image trunk and hidden representation.
    """
    is_ffr = is_ffr.bool()
    value = value.to(outputs["ffr"].dtype)
    predicted_value = torch.where(is_ffr, outputs["ffr"], outputs["ifr"])
    logit = torch.where(is_ffr, outputs["ffr_logit"], outputs["ifr_logit"])
    valid = torch.isfinite(value) & (value > 0) & (value <= 1.05)
    if not valid.any():
        return sum(v.sum() for v in outputs.values()) * 0
    if abnormal is None:
        threshold = torch.where(is_ffr, value.new_tensor(0.80), value.new_tensor(0.89))
        abnormal = value <= threshold
    reg = F.smooth_l1_loss(predicted_value[valid], value[valid], beta=0.05)
    binary = F.binary_cross_entropy_with_logits(logit[valid], abnormal[valid].to(logit.dtype))
    return binary + regression_weight * reg
