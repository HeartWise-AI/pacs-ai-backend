"""Backward-compatible v2: intermediate spatial tokens from one encoder call.

The default taps are post-block 5/11/17 (zero based) plus the encoder's final
normalized output. Hooks exist only during the full-view encoder call and are
removed even if it fails. Crops still use the same encoder once, without taps.

Zero residual gates preserve the v1 decoder exactly at upgrade time. Opening
these gates is a controlled localization experiment, not a validated gain.
V1 bundles, caches, model files, and result artifacts are never overwritten.
"""
from __future__ import annotations

from dataclasses import dataclass
import hashlib
import json
from pathlib import Path
from typing import Sequence

import torch
from torch import Tensor, nn
import torch.nn.functional as F

from integrated_model import (
    ALL_SEGMENTS, IntegratedStudyModel, N_CLASSES, PATCH, TUBELET,
    TokenMaskDecoder, load_encoder, normalize_clip,
)

DEFAULT_LAYERS = (5, 11, 17)
CACHE_SCHEMA = "deepcoro.spatial_tokens.multilayer.v2"
BUNDLE_SCHEMA = "deepcoro.integrated_study.multilayer.v2"
DECODER_SCHEMA = "deepcoro.mask_decoder.multilayer.v2"


def validate_layers(indices: Sequence[int], depth: int) -> tuple[int, ...]:
    layers = tuple(int(i) for i in indices)
    if not layers or layers != tuple(sorted(set(layers))):
        raise ValueError("Intermediate block indices must be nonempty, unique, and increasing")
    if layers[0] < 0 or layers[-1] >= depth-1:
        raise ValueError("Tap intermediate blocks before the final block; final tokens are stored separately")
    return layers


@dataclass(frozen=True)
class SpatialTokenPyramid:
    final: Tensor
    intermediate: tuple[Tensor, ...]
    block_indices: tuple[int, ...]

    def temporal_mean(self, temporal_tokens: int):
        """Still-image cache representation; token order remains h,w."""
        b, n, d = self.final.shape
        if n % temporal_tokens:
            raise ValueError("Token count does not match the temporal grid")
        def reduce(x):
            return x.reshape(b, temporal_tokens, n//temporal_tokens, d).float().mean(1)
        return SpatialTokenPyramid(reduce(self.final), tuple(reduce(x) for x in self.intermediate), self.block_indices)

    def stacked(self):
        """(B,1+layers,N,D), final first; ordering is part of the schema."""
        return torch.stack((self.final, *self.intermediate), dim=1)


def encoder_spatial_pyramid(encoder: nn.Module, normalized_clip: Tensor,
                            block_indices: Sequence[int] = DEFAULT_LAYERS) -> SpatialTokenPyramid:
    """Collect raw post-block tokens during exactly one call to ``encoder``."""
    layers = validate_layers(block_indices, len(encoder.blocks))
    if getattr(encoder, "out_layers", None) is not None:
        raise ValueError("The shared encoder must return its final tensor, not an out_layers list")
    captured, counts, handles = {}, {}, []
    def tap(index):
        def hook(module, inputs, output):
            if not isinstance(output, Tensor) or output.ndim != 3:
                raise TypeError(f"Block {index} did not return (B,N,D) spatial tokens")
            captured[index] = output  # keep gradients during joint training
            counts[index] = counts.get(index, 0)+1
        return hook
    try:
        for index in layers:
            handles.append(encoder.blocks[index].register_forward_hook(tap(index)))
        final = encoder(normalized_clip)
    finally:
        for handle in handles:
            handle.remove()
    if not isinstance(final, Tensor) or final.ndim != 3:
        raise TypeError("The encoder did not return final (B,N,D) tokens")
    if set(captured) != set(layers) or any(counts[i] != 1 for i in layers):
        raise RuntimeError("Each selected intermediate block must execute exactly once in the encoder forward")
    if any(captured[i].shape != final.shape for i in layers):
        raise ValueError("Intermediate/final token grids or embedding dimensions differ")
    return SpatialTokenPyramid(final, tuple(captured[i] for i in layers), layers)


def feature_spec(*, block_indices: Sequence[int], encoder_depth: int, embed_dim: int,
                 resolution: int, base_encoder_sha256: str, encoder_init_sha256: str | None,
                 encoder_implementation_sha256: str | None = None,
                 encoder_bundle_sha256: str | None = None, compute_precision="float32"):
    """Everything that changes the numerical meaning/order of cached tokens."""
    layers = validate_layers(block_indices, encoder_depth)
    if resolution % PATCH:
        raise ValueError("Resolution must be a multiple of the encoder patch size")
    return {
        "schema": CACHE_SCHEMA,
        "encoder": {"architecture": "EchoJEPA-vIT-L-RoPE", "depth": encoder_depth,
                    "embed_dim": embed_dim, "base_sha256": base_encoder_sha256,
                    "init_sha256": encoder_init_sha256,
                    "bundle_sha256": encoder_bundle_sha256,
                    "patch_size": PATCH, "tubelet_size": TUBELET,
                    "use_rope": True, "uniform_power": True, "use_sdpa": True,
                    "implementation_sha256": encoder_implementation_sha256},
        "block_indices_zero_based": list(layers),
        "feature_order": ["tokens"]+[f"tokens_block_{i:03d}" for i in layers],
        "normalization": {"tokens": "encoder final norm, then temporal mean",
                          "intermediate": "raw post-block, before final norm, then temporal mean",
                          "decoder": "independent LayerNorm and linear projection per feature"},
        "grid": [1, resolution//PATCH, resolution//PATCH],
        "token_order": "h,w after mean over 8 temporal tubelets; forward order before mean is t,h,w",
        "temporal_input": {"frames": 16, "tubelet": TUBELET, "mode": "repeat identical still image"},
        "input": {"resolution": resolution, "grayscale_scale": "uint8 / 255",
                  "channel_mean": [.485, .456, .406], "channel_std": [.229, .224, .225]},
        "storage_dtype": "float16", "storage_layout": "separate NPZ arrays (N,D)",
        "compute_precision": compute_precision, "torch_version": str(torch.__version__),
    }


def feature_fingerprint(spec: dict) -> str:
    canonical = json.dumps(spec, sort_keys=True, separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(canonical.encode()).hexdigest()


def validate_feature_metadata(metadata: dict, expected_layers: Sequence[int] | None = None):
    if metadata.get("cache_schema") != CACHE_SCHEMA:
        raise ValueError("A genuine multilayer v2 cache is required; v1 pooled/final-only caches are incompatible")
    spec = metadata.get("feature_spec")
    if not isinstance(spec, dict) or spec.get("schema") != CACHE_SCHEMA:
        raise ValueError("Missing or incompatible multilayer feature specification")
    if metadata.get("feature_fingerprint") != feature_fingerprint(spec):
        raise ValueError("Multilayer feature fingerprint does not match its specification")
    layers = tuple(spec["block_indices_zero_based"])
    validate_layers(layers, spec["encoder"]["depth"])
    if expected_layers is not None and layers != tuple(expected_layers):
        raise ValueError("Cached intermediate block order differs from the requested decoder")
    expected_keys = ["tokens"]+[f"tokens_block_{i:03d}" for i in layers]
    if spec["feature_order"] != expected_keys:
        raise ValueError("Multilayer cache feature order does not match its named block indices")
    reference = feature_spec(block_indices=layers, encoder_depth=spec["encoder"]["depth"],
                             embed_dim=spec["encoder"]["embed_dim"], resolution=spec["input"]["resolution"],
                             base_encoder_sha256=spec["encoder"]["base_sha256"],
                             encoder_init_sha256=spec["encoder"].get("init_sha256"))
    for key in ("normalization", "grid", "token_order", "temporal_input", "input", "storage_dtype", "storage_layout"):
        if spec.get(key) != reference[key]:
            raise ValueError(f"Unsupported multilayer feature convention: {key}")
    return spec


class MultiLayerTokenMaskDecoder(TokenMaskDecoder):
    """V1 decoder plus gated residual adapters for intermediate spatial layers."""
    def __init__(self, embed_dim=1024, width=128, block_indices=DEFAULT_LAYERS):
        super().__init__(embed_dim, width)
        self.block_indices = tuple(block_indices)
        self.intermediate_proj = nn.ModuleDict({str(i): nn.Sequential(
            nn.LayerNorm(embed_dim), nn.Linear(embed_dim, width), nn.GELU(),
        ) for i in self.block_indices})
        self.intermediate_gate = nn.Parameter(torch.zeros(len(self.block_indices)))

    def import_v1(self, state_dict):
        if any(k == "intermediate_gate" or k.startswith("intermediate_proj.") for k in state_dict):
            raise ValueError("This decoder already has intermediate branches; load it explicitly as v2")
        missing, unexpected = self.load_state_dict(state_dict, strict=False)
        if unexpected or any(not (k == "intermediate_gate" or k.startswith("intermediate_proj.")) for k in missing):
            raise ValueError(f"Incompatible v1 decoder weights: missing={missing}, unexpected={unexpected}")
        self.intermediate_gate.data.zero_()

    def forward(self, tokens: Tensor, frames: Tensor, grid: tuple[int, int, int],
                intermediate: Sequence[Tensor] | None = None) -> Tensor:
        # Packed form lets the unchanged v1 loss/evaluation utilities consume
        # v2 caches without mistaking final-only tokens for a multilayer cache.
        if tokens.ndim == 4:
            if intermediate is not None or tokens.shape[1] != len(self.block_indices)+1:
                raise ValueError("Packed features must contain final tokens followed by each configured layer")
            intermediate = tuple(tokens[:, i+1] for i in range(len(self.block_indices)))
            tokens = tokens[:, 0]
        if intermediate is None or len(intermediate) != len(self.block_indices):
            raise ValueError("Every configured intermediate layer is required by the v2 decoder")
        b, t, h, w = frames.shape
        tt, gh, gw = grid
        if tokens.shape != (b, tt*gh*gw, self.embed_dim) or any(x.shape != tokens.shape for x in intermediate):
            raise ValueError("Spatial feature shapes disagree with the decoder grid")
        if t % tt:
            raise ValueError("Frame count must be a multiple of the temporal token grid")
        z = self.token_proj(tokens)
        for j, (index, feature) in enumerate(zip(self.block_indices, intermediate)):
            residual = self.intermediate_proj[str(index)](feature).to(z.dtype)
            z = z+torch.tanh(self.intermediate_gate[j]).to(z.dtype)*residual
        z = z.reshape(b, tt, gh, gw, self.width).permute(0, 1, 4, 2, 3)
        z = z.repeat_interleave(t//tt, dim=1).reshape(b*t, self.width, gh, gw)
        im = frames.reshape(b*t, 1, h, w)
        d1 = self.detail1(im)
        d2 = self.detail2(d1)
        z = F.interpolate(z, d2.shape[-2:], mode="bilinear", align_corners=False)
        z = self.fuse2(torch.cat((z, d2), dim=1))
        z = F.interpolate(z, d1.shape[-2:], mode="bilinear", align_corners=False)
        z = self.fuse1(torch.cat((z, d1), dim=1))
        z = F.interpolate(self.out(z), (h, w), mode="bilinear", align_corners=False)
        return z.reshape(b, t, N_CLASSES, h, w)


class MultiLayerStudyModel(IntegratedStudyModel):
    def __init__(self, encoder, groups=ALL_SEGMENTS, train_blocks=0,
                 decoder_width=128, block_indices=DEFAULT_LAYERS):
        layers = validate_layers(block_indices, len(encoder.blocks))
        super().__init__(encoder, groups, train_blocks, decoder_width)
        v1 = self.mask_decoder
        self.mask_decoder = MultiLayerTokenMaskDecoder(encoder.embed_dim, decoder_width, layers)
        self.mask_decoder.import_v1(v1.state_dict())
        self.block_indices = layers
        self._capture_next = False
        self._pending = None
        self._study_active = False

    def encode_features(self, frames):
        return encoder_spatial_pyramid(self.enc, normalize_clip(frames), self.block_indices)

    def encode(self, frames):
        if not self._capture_next:
            return super().encode(frames)  # crop pass: no hooks or feature retention
        pyramid = self.encode_features(frames)
        self._pending = pyramid
        self._capture_next = False
        return pyramid.final

    def decode(self, tokens, frames):
        pyramid = self._pending
        self._pending = None
        if pyramid is None or pyramid.final is not tokens:
            raise RuntimeError("Dense decoding must consume features from the immediately preceding shared encoder call")
        return self.mask_decoder(tokens, frames,
                                 (frames.shape[1]//TUBELET, frames.shape[-2]//PATCH, frames.shape[-1]//PATCH),
                                 intermediate=pyramid.intermediate)

    def forward_study(self, clips, **kwargs):
        if self._study_active:
            raise RuntimeError("Concurrent study calls on one model instance are unsupported; batch its views in one call")
        self._study_active, self._capture_next = True, True
        try:
            result = super().forward_study(clips, **kwargs)
            result["decoder_variant"] = "multilayer_v2"
            result["intermediate_blocks_zero_based"] = self.block_indices
            return result
        finally:
            self._capture_next = False
            self._pending = None
            self._study_active = False


def upgrade_v1_bundle(checkpoint: str | Path, device="cpu", train_blocks=0,
                      block_indices=DEFAULT_LAYERS):
    """Import all v1 weights into an exact-function, zero-gate v2 warm start."""
    payload = torch.load(checkpoint, map_location="cpu", weights_only=False)
    if payload.get("bundle_schema") == BUNDLE_SCHEMA:
        raise ValueError("This is already a v2 bundle; use load_v2_bundle")
    return multilayer_model_from_payload(payload, device=device, train_blocks=train_blocks,
                                         upgrade_layers=block_indices), payload


def multilayer_model_from_payload(payload, device="cpu", train_blocks=0, upgrade_layers=None):
    """Construct from an already loaded payload, avoiding duplicate 1.2GB reads."""
    is_v2 = payload.get("bundle_schema") == BUNDLE_SCHEMA
    if not is_v2 and upgrade_layers is None:
        raise ValueError("An explicit layer selection is required to upgrade a v1 bundle")
    layers = payload["block_indices_zero_based"] if is_v2 else upgrade_layers
    encoder = load_encoder(None, "cpu")
    model = MultiLayerStudyModel(encoder, payload["groups"], train_blocks,
                                 payload["decoder_width"], layers)
    if payload.get("physiology"):
        from physiology_head import PhysiologyHead
        model.physiology_head = PhysiologyHead(**payload["physiology"])
    if any(k.startswith("procedure_head.") for k in payload["state_dict"]):
        model.enable_procedure_head()
    if any(k.startswith("lesion_head.") for k in payload["state_dict"]):
        model.enable_lesion_head()
    missing, unexpected = model.load_state_dict(payload["state_dict"], strict=is_v2)
    if unexpected or any(not (k == "mask_decoder.intermediate_gate" or k.startswith("mask_decoder.intermediate_proj.")) for k in missing):
        raise ValueError(f"V1 bundle structure mismatch: missing={missing}, unexpected={unexpected}")
    return model.to(device)


def load_v2_bundle(checkpoint: str | Path, device="cpu", train_blocks=0):
    payload = torch.load(checkpoint, map_location="cpu", weights_only=False)
    if payload.get("bundle_schema") != BUNDLE_SCHEMA:
        raise ValueError("Expected a multilayer v2 bundle; upgrade v1 explicitly")
    return multilayer_model_from_payload(payload, device=device, train_blocks=train_blocks), payload


def v2_bundle_payload(model, parent_payload, *, parent_sha256, feature_metadata=None):
    return {
        "bundle_schema": BUNDLE_SCHEMA, "state_dict": {k:v.detach().cpu() for k,v in model.state_dict().items()},
        "groups": model.groups, "decoder_width": model.mask_decoder.width,
        "block_indices_zero_based": list(model.block_indices), "physiology": parent_payload.get("physiology"),
        "feature_metadata": feature_metadata,
        "lineage": {**parent_payload.get("lineage", {}), "parent_v1_bundle_sha256": parent_sha256,
                    "decoder_variant": "multilayer_v2", "validated": False,
                    "upgrade_function": "exact v1 warm start through zero intermediate residual gates"},
    }
