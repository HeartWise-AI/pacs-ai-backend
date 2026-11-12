"""One shared EchoJEPA encoder, a dense mask decoder, and study MIL heads.

Deployment accepts raw grayscale clips. All full views are encoded in one batch
(pass 1); the model's masks determine tracked native-resolution crops, which are
encoded together with the *same* encoder (pass 2). No teacher, cached tokens,
cached crops, or VasoVision outputs are used by ``forward_study``.

The old MIL2 checkpoints are warm starts, not validated integrated checkpoints:
their full-view tokens came from a different frozen encoder and teacher masks.
Joint fine-tuning and a fresh validation are required before claiming any gate.
"""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import sys
from typing import Sequence

import numpy as np
import torch
from torch import Tensor, nn
import torch.nn.functional as F

N_CLASSES = 26
PATCH = 16
TUBELET = 2
N_FRAMES = 16
ALL_SEGMENTS = (
    "prox_rca", "mid_rca", "dist_rca", "pda", "posterolateral", "left_main",
    "prox_lad", "mid_lad", "dist_lad", "D1", "D2", "prox_lcx", "mid_lcx",
    "dist_lcx", "om1", "om2", "bx", "lvp",
)
SEGMENT_CLASSES = {
    "prox_rca": (1,), "mid_rca": (2,), "dist_rca": (3,), "pda": (4, 19),
    "posterolateral": (20, 21, 22, 23), "left_main": (5,), "prox_lad": (6,),
    "mid_lad": (7,), "dist_lad": (8,), "D1": (9, 10), "D2": (11, 12),
    "prox_lcx": (13,), "mid_lcx": (13, 16), "dist_lcx": (16,),
    "om1": (15,), "om2": (24,), "bx": (14,), "lvp": (17, 18, 25),
}
DISTAL_CLASSES = (4, 8, 16, 19, 20, 21, 22, 23)


def load_encoder(checkpoint: str | Path | None, device: str | torch.device = "cpu"):
    """Load exactly the released EJ-FT encoder, without segmentation imports."""
    sys.path.insert(0, str(Path(__file__).resolve().parent / "echojepa_src"))  # vendored EchoJEPA ViT code (MIT, see echojepa_src/LICENSE)
    from src.models import vision_transformer as vit
    enc = vit.vit_large(
        patch_size=PATCH, img_size=(224, 224), num_frames=N_FRAMES,
        tubelet_size=TUBELET, use_rope=True, uniform_power=True, use_sdpa=True,
    )
    if checkpoint is not None:
        enc.load_state_dict(torch.load(checkpoint, map_location="cpu", weights_only=True), strict=True)
    return enc.to(device)


def normalize_clip(frames: Tensor) -> Tensor:
    """(B,T,H,W) grayscale [0,1] -> EchoJEPA (B,3,T,H,W)."""
    x = frames[:, None].expand(-1, 3, -1, -1, -1)
    mean = x.new_tensor((0.485, 0.456, 0.406)).view(1, 3, 1, 1, 1)
    std = x.new_tensor((0.229, 0.224, 0.225)).view(1, 3, 1, 1, 1)
    return (x - mean) / std


def token_fractions(prob: Tensor, grid: tuple[int, int, int]) -> Tensor:
    """(B,T,C,H,W) probabilities -> (B,t*h*w,C), token order t,h,w."""
    b, t, c, h, w = prob.shape
    tt, gh, gw = grid
    if t % tt:
        raise ValueError(f"Cannot pool {t} frames onto {tt} temporal tokens")
    p = F.adaptive_avg_pool2d(prob.reshape(b * t, c, h, w), (gh, gw))
    p = p.reshape(b, tt, t // tt, c, gh, gw).mean(2)
    return p.permute(0, 1, 3, 4, 2).reshape(b, tt * gh * gw, c)


class ConvBlock(nn.Sequential):
    def __init__(self, c_in: int, c_out: int, stride: int = 1):
        super().__init__(
            nn.Conv2d(c_in, c_out, 3, stride=stride, padding=1, bias=False),
            nn.GroupNorm(8, c_out), nn.GELU(),
            nn.Conv2d(c_out, c_out, 3, padding=1, bias=False),
            nn.GroupNorm(8, c_out), nn.GELU(),
        )


class TokenMaskDecoder(nn.Module):
    """Token-conditioned dense decoder with grayscale detail skips.

    The skips are layers of this model, not a separately pretrained network.
    ARCADE still images use the average of repeated-image temporal tokens; video
    inference uses each tubelet's tokens for its two constituent frames.
    """
    def __init__(self, embed_dim: int = 1024, width: int = 128):
        super().__init__()
        self.embed_dim, self.width = embed_dim, width
        self.token_proj = nn.Sequential(nn.LayerNorm(embed_dim), nn.Linear(embed_dim, width), nn.GELU())
        self.detail1 = ConvBlock(1, 32, stride=2)
        self.detail2 = ConvBlock(32, 48, stride=2)
        self.fuse2 = ConvBlock(width + 48, width)
        self.fuse1 = ConvBlock(width + 32, 64)
        self.out = nn.Conv2d(64, N_CLASSES, 1)

    def forward(self, tokens: Tensor, frames: Tensor,
                grid: tuple[int, int, int]) -> Tensor:
        b, t, h, w = frames.shape
        tt, gh, gw = grid
        if tokens.shape != (b, tt * gh * gw, self.embed_dim):
            raise ValueError(f"Token shape {tuple(tokens.shape)} disagrees with grid {grid}")
        if t % tt:
            raise ValueError("Frame count must be a multiple of the temporal token grid")
        z = self.token_proj(tokens).reshape(b, tt, gh, gw, self.width)
        z = z.permute(0, 1, 4, 2, 3).repeat_interleave(t // tt, dim=1)
        z = z.reshape(b * t, self.width, gh, gw)
        im = frames.reshape(b * t, 1, h, w)
        d1 = self.detail1(im)
        d2 = self.detail2(d1)
        z = F.interpolate(z, d2.shape[-2:], mode="bilinear", align_corners=False)
        z = self.fuse2(torch.cat((z, d2), dim=1))
        z = F.interpolate(z, d1.shape[-2:], mode="bilinear", align_corners=False)
        z = self.fuse1(torch.cat((z, d1), dim=1))
        z = F.interpolate(self.out(z), (h, w), mode="bilinear", align_corners=False)
        return z.reshape(b, t, N_CLASSES, h, w)


@dataclass
class CropSpec:
    video: int
    group: int
    boxes: Tensor  # (T,4), native pixel y,x,height,width
    mass: float


def _tracked_boxes(mask: np.ndarray, native_hw: tuple[int, int], min_pixels: int,
                   margin: float = 0.035, min_side: float = 0.19):
    """Track a mask without depending on the external segmentation package."""
    import cv2
    t, rh, rw = mask.shape
    h, w = native_hw
    union = mask.any(0)
    n, comp = cv2.connectedComponents(union.astype(np.uint8), connectivity=8)
    keep = np.zeros_like(union)
    for k in range(1, n):
        part = comp == k
        if mask[:, part].any(1).sum() < min(3, t):
            continue
        yy, xx = np.nonzero(part)
        if len(yy) > 20:
            ev = np.linalg.eigvalsh(np.cov(np.stack((yy, xx))))
            if ev[-1] / max(ev.sum(), 1e-9) > 0.995 and 4 * np.sqrt(ev[-1]) > rh * 0.29:
                continue
        keep |= part
    mask = mask & keep[None]
    if mask.sum() * (h * w / (rh * rw)) < min_pixels:
        return None
    centre = np.full((t, 2), np.nan)
    extents = []
    for f in range(t):
        yy, xx = np.nonzero(mask[f])
        if len(yy):
            centre[f] = (yy.mean() * h / rh, xx.mean() * w / rw)
            extents.append(max(np.ptp(yy) * h / rh, np.ptp(xx) * w / rw))
    valid = np.isfinite(centre[:, 0])
    if not valid.any():
        return None
    for j in range(2):
        centre[:, j] = np.interp(np.arange(t), np.flatnonzero(valid), centre[valid, j])
        centre[:, j] = np.convolve(np.pad(centre[:, j], 1, mode="edge"), np.ones(3) / 3, mode="valid")
    side = min(max(float(np.percentile(extents, 90)) + 2 * margin * min(h, w),
                   min_side * min(h, w)), h, w)
    top = np.clip(centre[:, 0] - side / 2, 0, h - side)
    left = np.clip(centre[:, 1] - side / 2, 0, w - side)
    boxes = np.stack((top, left, np.full(t, side), np.full(t, side)), axis=1)
    return boxes.astype(np.float32)


def crop_tensor(clip: Tensor, boxes: Tensor, output_res: int) -> Tensor:
    """Sample tracked native boxes, (T,H,W) -> (T,R,R)."""
    t, h, w = clip.shape
    # align_corners=False maps pixel centres; boxes may be fractional.
    v = (torch.arange(output_res, device=clip.device, dtype=torch.float32) + 0.5) / output_res
    yy = boxes[:, 0, None, None] + boxes[:, 2, None, None] * v[None, :, None]
    xx = boxes[:, 1, None, None] + boxes[:, 3, None, None] * v[None, None, :]
    yy = (yy / h * 2 - 1).expand(t, output_res, output_res)
    xx = (xx / w * 2 - 1).expand(t, output_res, output_res)
    grid = torch.stack((xx, yy), dim=-1)
    return F.grid_sample(clip[:, None].float(), grid, mode="bilinear",
                         padding_mode="border", align_corners=False)[:, 0]


PROCEDURE_OUTPUTS = ("stent", "timi", "failed")  # per segment: stent/PCI-treated visible; final TIMI < 3; PCI failed
LESION_OUTPUTS = ("calcif", "thrombus", "cto")  # v11 per segment: moderate+ calcification (CLIP definition), thrombus, CTO


class ProcedureHead(nn.Module):
    """Per-segment procedure logits from each segment's pooled study feature (LayerNorm + per-segment linear)."""
    def __init__(self, d: int, ng: int, n_out: int = len(PROCEDURE_OUTPUTS)):
        super().__init__()
        self.norm = nn.LayerNorm(d)
        self.weight = nn.Parameter(torch.zeros(n_out, ng, d))
        self.bias = nn.Parameter(torch.full((n_out, ng), -4.0))

    def forward(self, features: Tensor) -> Tensor:
        """(groups, d) -> (n_out, groups) logits."""
        return (self.norm(features.to(self.weight.dtype))[None] * self.weight).sum(-1) + self.bias


class IntegratedStudyModel(nn.Module):
    def __init__(self, encoder: nn.Module, groups: Sequence[str] = ALL_SEGMENTS,
                 train_blocks: int = 0, decoder_width: int = 128):
        super().__init__()
        self.enc = encoder
        self.groups = tuple(groups)
        if any(g not in SEGMENT_CLASSES for g in self.groups):
            raise ValueError(f"Unknown segment groups {self.groups}")
        d, ng = encoder.embed_dim, len(self.groups)
        for p in encoder.parameters():
            p.requires_grad_(False)
        if train_blocks:
            for block in encoder.blocks[-train_blocks:]:
                block.requires_grad_(True)
            if getattr(encoder, "norm", None) is not None:
                encoder.norm.requires_grad_(True)
        self.mask_decoder = TokenMaskDecoder(d, decoder_width)
        self.q = nn.Parameter(torch.zeros(ng, d))
        self.mask_w = nn.Parameter(torch.ones(ng))
        self.head = nn.ModuleList([
            nn.Sequential(nn.LayerNorm(d), nn.Linear(d, 256), nn.GELU(), nn.Dropout(0.2), nn.Linear(256, 2))
            for _ in self.groups
        ])
        self.att_v = nn.Linear(d, 128)
        self.att_u = nn.Linear(d, 128)
        self.att_w = nn.Linear(128, 1)
        self.view_proj = nn.Sequential(nn.LayerNorm(2 * d + 2), nn.Linear(2 * d + 2, d), nn.GELU(), nn.Linear(d, d))
        self.type_emb = nn.Parameter(torch.zeros(2, d))
        self.group_emb = nn.Parameter(torch.zeros(ng, d))
        self.physiology_head = None
        self.procedure_head = None  # optional (v10): per-segment stent / final TIMI<3 / PCI-failed logits
        self.lesion_head = None  # optional (v11): per-segment calcification / thrombus / CTO logits

    def enable_procedure_head(self):
        """Per-segment linear logits on the pooled segment feature; initialised to a ~2% prior."""
        d, ng = self.q.shape[1], len(self.groups)
        self.procedure_head = ProcedureHead(d, ng).to(self.q.device)
        return self.procedure_head

    def enable_lesion_head(self):
        """Per-segment linear calcification / thrombus / CTO logits on the pooled segment feature (~2% prior)."""
        d, ng = self.q.shape[1], len(self.groups)
        self.lesion_head = ProcedureHead(d, ng, len(LESION_OUTPUTS)).to(self.q.device)
        return self.lesion_head

    def encode(self, frames: Tensor) -> Tensor:
        return self.enc(normalize_clip(frames))

    def decode(self, tokens: Tensor, frames: Tensor) -> Tensor:
        return self.mask_decoder(tokens, frames,
                                 (frames.shape[1] // TUBELET, frames.shape[-2] // PATCH, frames.shape[-1] // PATCH))

    def warm_start_mil2(self, checkpoint: str | Path, old_groups: Sequence[str]):
        """Copy compatible heads and trained encoder blocks by segment name."""
        sd = torch.load(checkpoint, map_location="cpu", weights_only=True)
        own = self.state_dict()
        copied = []
        for key, val in sd.items():
            if key in ("q", "mask_w", "group_emb") or key.startswith("head."):
                continue
            if key in own and own[key].shape == val.shape:
                own[key] = val
                copied.append(key)
        for ni, name in enumerate(self.groups):
            if name not in old_groups:
                continue
            oi = list(old_groups).index(name)
            for key in ("q", "mask_w", "group_emb"):
                if key in sd:
                    own[key][ni] = sd[key][oi]
            for key, val in sd.items():
                if key.startswith(f"head.{oi}."):
                    own[f"head.{ni}." + key.split(".", 2)[2]] = val
        self.load_state_dict(own, strict=True)
        return copied

    def _pool_study(self, tokens: Tensor, fractions: Tensor,
                    crop_tokens: Tensor | None, crop_fractions: Tensor | None,
                    specs: list[CropSpec]) -> tuple[Tensor, Tensor, list[Tensor]]:
        """Pool one study's videos and optional crops for every report segment."""
        vg = tokens.mean(1)
        predictions, study_features, bag_attention = [], [], []
        for gi, group in enumerate(self.groups):
            fr = fractions[:, :, SEGMENT_CLASSES[group]].sum(-1)
            mass = fr.sum(1)
            pooled = (fr[:, :, None].to(tokens.dtype) * tokens).sum(1) / mass[:, None].clamp_min(1e-6)
            view = torch.cat((pooled, vg, torch.log1p(mass)[:, None], torch.ones_like(mass[:, None])), dim=-1)
            view = self.view_proj(view.to(self.q.dtype)) + self.group_emb[gi] + self.type_emb[1]
            chosen = [i for i, s in enumerate(specs) if s.group == gi]
            if chosen:
                ct = crop_tokens[chosen]
                cf = crop_fractions[chosen]
                logits = (ct * self.q[gi][None, None]).sum(-1) / ct.shape[-1] ** 0.5
                logits = logits + self.mask_w[gi] * (cf + 1e-3).log()
                crop = (logits.softmax(-1)[:, :, None].to(ct.dtype) * ct).sum(1) + self.type_emb[0]
                instances = torch.cat((crop.to(view.dtype), view), dim=0)
            else:
                instances = view
            a = self.att_w(torch.tanh(self.att_v(instances)) * torch.sigmoid(self.att_u(instances)))[:, 0].float().softmax(0)
            feat = (a[:, None].to(instances.dtype) * instances).sum(0)
            predictions.append(self.head[gi](feat))
            study_features.append(feat)
            bag_attention.append(a)
        return torch.stack(predictions), torch.stack(study_features), bag_attention

    def forward_study(self, clips: Sequence[Tensor], full_res: int = 224,
                      crop_res: int = 224, crops_per_group: int = 1,
                      max_full_views: int = 12, min_pixels: int = 200,
                      measurements: Tensor | None = None,
                      modality: Tensor | None = None) -> dict:
        """Raw native clips (each 16,H,W in [0,1]) -> all study predictions.

        Study batching is intentionally explicit: one call holds one study, all
        its full views in one encoder call and all its selected crops in a second
        call. Reducing ``max_full_views``/``crops_per_group`` controls memory;
        chunking the encoder would introduce additional network passes.
        """
        if not clips:
            raise ValueError("A study must contain at least one diagnostic clip")
        if full_res % PATCH or crop_res % PATCH:
            raise ValueError("EchoJEPA resolutions must be multiples of 16")
        if max_full_views < 1 or crops_per_group < 0:
            raise ValueError("max_full_views must be positive; crops_per_group must be nonnegative")
        clips = list(clips)[:max_full_views]
        dev = self.q.device
        clips = [x.to(device=dev, dtype=torch.float32) for x in clips]
        if any(x.ndim != 3 or x.shape[0] != N_FRAMES for x in clips):
            raise ValueError("Each clip must have shape (16,H,W)")
        full = torch.stack([F.interpolate(x[:, None], (full_res, full_res), mode="bilinear", align_corners=False)[:, 0]
                            for x in clips])
        tokens = self.encode(full)  # ENCODER PASS 1
        mask_logits = self.decode(tokens, full)
        prob = mask_logits.float().softmax(2)
        fractions = token_fractions(prob, (N_FRAMES // 2, full_res // PATCH, full_res // PATCH))
        labels = prob.argmax(2).detach().cpu().numpy()
        specs = []
        for gi, group in enumerate(self.groups):
            candidates = []
            for vi, raw in enumerate(clips):
                m = np.isin(labels[vi], SEGMENT_CLASSES[group])
                boxes = _tracked_boxes(m, tuple(raw.shape[-2:]), min_pixels)
                if boxes is not None:
                    candidates.append(CropSpec(vi, gi, torch.from_numpy(boxes).to(dev), float(m.sum())))
            specs.extend(sorted(candidates, key=lambda s: -s.mass)[:crops_per_group])
        crop_tokens = crop_fr = None
        if specs:
            crop = torch.stack([crop_tensor(clips[s.video], s.boxes, crop_res) for s in specs])
            # Map native tracked boxes onto the full-view mask probability grid.
            fr_list = []
            for s in specs:
                h, w = clips[s.video].shape[-2:]
                scale = s.boxes.new_tensor((full_res / h, full_res / w, full_res / h, full_res / w))
                p = prob[s.video, :, SEGMENT_CLASSES[self.groups[s.group]], :, :].sum(1)
                cp = crop_tensor(p, s.boxes * scale, crop_res)
                fr_list.append(token_fractions(cp[None, :, None], (N_FRAMES // 2, crop_res // PATCH, crop_res // PATCH))[0, :, 0])
            crop_fr = torch.stack(fr_list)
            crop_tokens = self.encode(crop)  # ENCODER PASS 2, the SAME encoder
        pred, features, attention = self._pool_study(tokens, fractions, crop_tokens, crop_fr, specs)
        result = {
            "groups": self.groups, "severity": pred[:, 0] * 100,
            "binary_logits": pred[:, 1], "binary_probability": pred[:, 1].sigmoid(),
            "study_features": features, "mask_logits": mask_logits,
            "mask_fractions": fractions, "crop_specs": specs, "bag_attention": attention,
            "encoder_passes": 2 if specs else 1,
        }
        if self.procedure_head is not None:
            for name, logits in zip(PROCEDURE_OUTPUTS, self.procedure_head(features)):
                result[f"{name}_logits"], result[f"{name}_probability"] = logits, logits.sigmoid()
        if self.lesion_head is not None:
            for name, logits in zip(LESION_OUTPUTS, self.lesion_head(features)):
                result[f"lesion_{name}_logits"], result[f"lesion_{name}_probability"] = logits, logits.sigmoid()
        if self.physiology_head is not None:
            geometry_audit = None
            if self.physiology_head.geometry_dim and measurements is None:
                from physiology_mask_geometry import mask_geometry_vector
                measurements, geometry_audit = mask_geometry_vector(mask_logits, return_audit=True)
            result["measurements"] = measurements
            result["geometry_audit"] = geometry_audit
            result["physiology"] = self.physiology_head(
                features[None], geometry=measurements, modality=modality,
            )
        return result

    def forward(self, clips: Sequence[Tensor], **kwargs):
        return self.forward_study(clips, **kwargs)


def segmentation_loss(logits: Tensor, target: Tensor, class_weights: Tensor,
                      teacher_prob: Tensor | None = None, agreement: Tensor | None = None,
                      distill_weight: float = 1.0, hard_weight: Tensor | None = None,
                      cldice_weight: float = 0.1) -> tuple[Tensor, dict[str, float]]:
    """Weighted CE + foreground Dice + optional agreement-weighted KL.

    logits: B,T,C,H,W; hard targets B,T,H,W. Teacher probabilities have the
    same shape as logits; agreement is B,T,H,W in [0,1]. Hard targets may be
    -1 at pixels with no human annotation. Distillation never fabricates soft
    probabilities from an argmax-only cache.
    """
    b, t, c, h, w = logits.shape
    z = logits.float().reshape(b * t, c, h, w)
    y = target.long().reshape(b * t, h, w)
    valid = y >= 0
    hw = valid.float() if hard_weight is None else hard_weight.float().reshape_as(y) * valid
    if valid.any():
        ce_pixel = F.cross_entropy(z, y, weight=class_weights, ignore_index=-1, reduction="none")
        ce = (ce_pixel * hw).sum() / (class_weights[y.clamp_min(0)] * hw).sum().clamp_min(1e-6)
    else:
        ce = z.sum() * 0
    p = z.softmax(1)
    oh = F.one_hot(y.clamp_min(0), c).permute(0, 3, 1, 2).float() * valid[:, None]
    inter = (p * oh * hw[:, None]).sum((0, 2, 3))
    den = ((p * valid[:, None] + oh) * hw[:, None]).sum((0, 2, 3))
    present = oh.sum((0, 2, 3)) > 0
    present[0] = False
    d = 1 - (2 * inter + 1) / (den + 1)
    dice = (d[present] * class_weights[present]).sum() / class_weights[present].sum().clamp_min(1e-6)
    kl = z.sum() * 0
    if teacher_prob is not None:
        q = teacher_prob.float().reshape_as(z).clamp_min(1e-7)
        q = q / q.sum(1, keepdim=True).clamp_min(1e-7)
        weight = torch.ones_like(y, dtype=torch.float32) if agreement is None else agreement.float().reshape_as(y)
        # Suppress background domination while retaining reliable negatives.
        anatomical_weight = (q * class_weights[None, :, None, None]).sum(1)
        weight = weight * anatomical_weight
        pixel_kl = (q * (q.log() - z.log_softmax(1))).sum(1)
        kl = (pixel_kl * weight).sum() / weight.sum().clamp_min(1e-6)
    cldice = z.sum() * 0
    if cldice_weight and (y > 0).any():
        # Vessel-tree connectivity is supervised jointly across anatomical
        # classes; neighbouring SYNTAX segments share a physical centreline.
        vessel = (1 - p[:, :1]) * valid[:, None]
        truth = (y > 0).float()[:, None]
        ps, ts = soft_skeleton(vessel), soft_skeleton(truth)
        precision = (ps * truth).sum() / ps.sum().clamp_min(1e-6)
        sensitivity = (ts * vessel).sum() / ts.sum().clamp_min(1e-6)
        cldice = 1 - (2 * precision * sensitivity + 1e-6) / (precision + sensitivity + 1e-6)
    loss = ce + dice + distill_weight * kl + cldice_weight * cldice
    return loss, {"ce": float(ce.detach()), "dice_loss": float(dice.detach()),
                  "kl": float(kl.detach()), "cldice_loss": float(cldice.detach())}


def soft_skeleton(image: Tensor, iterations: int = 5) -> Tensor:
    """Differentiable 2D morphological skeleton, evaluated on vessel probability."""
    def erode(x):
        return torch.minimum(-F.max_pool2d(-x, (3, 1), 1, (1, 0)),
                             -F.max_pool2d(-x, (1, 3), 1, (0, 1)))
    def opened(x):
        return F.max_pool2d(erode(x), 3, 1, 1)
    skel = F.relu(image - opened(image))
    for _ in range(iterations):
        image = erode(image)
        delta = F.relu(image - opened(image))
        skel = skel + F.relu(delta - skel * delta)
    return skel
