"""3D Axial RoPE (Rotary Position Embeddings) for video transformers.

This module implements 3D axial RoPE for spatiotemporal video data (T×H×W).
Based on the 2D axial RoPE from DINOv3 and NAVER's rope-vit, extended to 3D.
"""

import math
from typing import Tuple, Optional, Literal
import torch
import torch.nn as nn


def _rotate_half(x: torch.Tensor) -> torch.Tensor:
    """Rotate half the hidden dims of the input for RoPE."""
    x_even = x[..., 0::2]
    x_odd = x[..., 1::2]
    return torch.stack((-x_odd, x_even), dim=-1).flatten(-2)


def _build_freqs(
    dim: int,
    length: int,
    base: float = 10000.0,
    device: Optional[torch.device] = None,
    dtype: Optional[torch.dtype] = None
) -> torch.Tensor:
    """Build frequency tensor for RoPE."""
    inv_freq = 1.0 / (base ** (torch.arange(0, dim, 2, device=device, dtype=dtype) / dim))
    t = torch.arange(length, device=device, dtype=dtype)
    freqs = torch.einsum('n,d->nd', t, inv_freq)
    return torch.stack((freqs, freqs), dim=-1).reshape(length, dim)


class Rope3D(nn.Module):
    """3D axial RoPE for video transformers."""

    def __init__(
        self,
        embed_dim: int,
        num_heads: int,
        *,
        temporal_base: float = 10000.0,
        spatial_base: float = 10000.0,
        temporal_scale: float = 1.0,
        normalize_mode: Literal["separate", "max", "min"] = "separate",
        device: Optional[torch.device] = None,
        dtype: Optional[torch.dtype] = None,
    ):
        super().__init__()

        assert embed_dim % num_heads == 0, f"embed_dim ({embed_dim}) must be divisible by num_heads ({num_heads})"
        self.head_dim = embed_dim // num_heads

        if self.head_dim % 6 != 0:
            raise ValueError(
                f"For 3D RoPE, head_dim ({self.head_dim}) must be divisible by 6. "
                f"Got embed_dim={embed_dim}, num_heads={num_heads}"
            )

        self.num_heads = num_heads
        self.temporal_base = temporal_base
        self.spatial_base = spatial_base
        self.temporal_scale = temporal_scale
        self.normalize_mode = normalize_mode

        self.t_dim = self.head_dim // 3
        self.h_dim = self.head_dim // 3
        self.w_dim = self.head_dim - self.t_dim - self.h_dim

        assert self.t_dim % 2 == 0 and self.h_dim % 2 == 0 and self.w_dim % 2 == 0, (
            f"Each axis dimension must be even for rotation. Got T={self.t_dim}, "
            f"H={self.h_dim}, W={self.w_dim}"
        )

        self._cache = {}

    @torch.no_grad()
    def _get_cached_freqs(
        self,
        T: int,
        H: int,
        W: int,
        device: torch.device,
        dtype: torch.dtype,
        n_special: int = 0
    ) -> Tuple[torch.Tensor, torch.Tensor]:
        cache_key = (T, H, W, device, dtype, n_special)

        if not self.training and cache_key in self._cache:
            return self._cache[cache_key]

        t_freqs = _build_freqs(
            self.t_dim, T,
            self.temporal_base * self.temporal_scale,
            device, dtype
        )
        h_freqs = _build_freqs(
            self.h_dim, H,
            self.spatial_base,
            device, dtype
        )
        w_freqs = _build_freqs(
            self.w_dim, W,
            self.spatial_base,
            device, dtype
        )

        t_cos, t_sin = t_freqs.cos(), t_freqs.sin()
        h_cos, h_sin = h_freqs.cos(), h_freqs.sin()
        w_cos, w_sin = w_freqs.cos(), w_freqs.sin()

        t_cos = t_cos.view(T, 1, 1, self.t_dim).expand(T, H, W, -1)
        t_sin = t_sin.view(T, 1, 1, self.t_dim).expand(T, H, W, -1)

        h_cos = h_cos.view(1, H, 1, self.h_dim).expand(T, H, W, -1)
        h_sin = h_sin.view(1, H, 1, self.h_dim).expand(T, H, W, -1)

        w_cos = w_cos.view(1, 1, W, self.w_dim).expand(T, H, W, -1)
        w_sin = w_sin.view(1, 1, W, self.w_dim).expand(T, H, W, -1)

        cos = torch.cat([t_cos, h_cos, w_cos], dim=-1).reshape(T * H * W, self.head_dim)
        sin = torch.cat([t_sin, h_sin, w_sin], dim=-1).reshape(T * H * W, self.head_dim)

        if n_special > 0:
            special_cos = torch.ones((n_special, self.head_dim), device=device, dtype=dtype)
            special_sin = torch.zeros((n_special, self.head_dim), device=device, dtype=dtype)
            cos = torch.cat([special_cos, cos], dim=0)
            sin = torch.cat([special_sin, sin], dim=0)

        if not self.training:
            self._cache[cache_key] = (sin, cos)

        return sin, cos

    def forward(
        self,
        q: torch.Tensor,
        k: torch.Tensor,
        T: int,
        H: int,
        W: int,
        n_special: int = 0
    ) -> Tuple[torch.Tensor, torch.Tensor]:
        """Apply 3D RoPE to query and key tensors."""
        B, Hh, N, Dh = q.shape
        assert Dh == self.head_dim, f"Expected head_dim={self.head_dim}, got {Dh}"

        expected_spatial = T * H * W
        if n_special == 0 and N == expected_spatial + 1:
            n_special = 1

        if N != n_special + expected_spatial:
            return q, k

        sin, cos = self._get_cached_freqs(T, H, W, q.device, q.dtype, n_special)

        sin = sin.unsqueeze(0).unsqueeze(0)
        cos = cos.unsqueeze(0).unsqueeze(0)

        q_t, q_h, q_w = torch.split(q, [self.t_dim, self.h_dim, self.w_dim], dim=-1)
        k_t, k_h, k_w = torch.split(k, [self.t_dim, self.h_dim, self.w_dim], dim=-1)

        sin_t, sin_h, sin_w = torch.split(sin, [self.t_dim, self.h_dim, self.w_dim], dim=-1)
        cos_t, cos_h, cos_w = torch.split(cos, [self.t_dim, self.h_dim, self.w_dim], dim=-1)

        q_t = q_t * cos_t + _rotate_half(q_t) * sin_t
        q_h = q_h * cos_h + _rotate_half(q_h) * sin_h
        q_w = q_w * cos_w + _rotate_half(q_w) * sin_w

        k_t = k_t * cos_t + _rotate_half(k_t) * sin_t
        k_h = k_h * cos_h + _rotate_half(k_h) * sin_h
        k_w = k_w * cos_w + _rotate_half(k_w) * sin_w

        q_rot = torch.cat([q_t, q_h, q_w], dim=-1)
        k_rot = torch.cat([k_t, k_h, k_w], dim=-1)

        return q_rot, k_rot


def apply_rope_qk(
    q: torch.Tensor,
    k: torch.Tensor,
    sin: torch.Tensor,
    cos: torch.Tensor
) -> Tuple[torch.Tensor, torch.Tensor]:
    """Apply pre-computed RoPE sin/cos to Q and K tensors."""
    if sin.ndim == 2:
        sin = sin.unsqueeze(0).unsqueeze(0)
        cos = cos.unsqueeze(0).unsqueeze(0)

    q_rot = q * cos + _rotate_half(q) * sin
    k_rot = k * cos + _rotate_half(k) * sin

    return q_rot, k_rot
