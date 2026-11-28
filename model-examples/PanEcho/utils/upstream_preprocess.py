"""Upstream-aligned PanEcho input (PanEcho 1.1.3).

Up to 1.1.2 every clip was the whole 708x1016 GE screen (ECG trace, text, colour bar) squashed to
256x256, one centred 16-frame clip per video, and the study output was the softmax of the mean logits.
The PanEcho authors (CarDS-Yale) trained and evaluated on videos masked to the ultrasound sector,
4 clips per video, with probabilities averaged per study. This module reproduces that input from the
DICOM: crop to the 2D ultrasound regions (SequenceOfUltrasoundRegions, spatial format 1), pad to a
square, resize to 224 (correct aspect), and take 4 evenly spaced 16-frame clips at about 30 fps.
"""
from __future__ import annotations

from typing import List, Optional, Sequence, Tuple

import cv2
import numpy as np
import torch

CLIPS_PER_VIDEO = 4
CLIP_LEN = 16
SIZE = 224
IMAGENET_MEAN = torch.tensor([0.485, 0.456, 0.406]).view(3, 1, 1)
IMAGENET_STD = torch.tensor([0.229, 0.224, 0.225]).view(3, 1, 1)


def sector_box(dicom) -> Optional[Tuple[int, int, int, int]]:
    """Union box (x0, y0, x1, y1), exclusive end, of the 2D ultrasound regions; None when absent."""
    regions = [
        r for r in (getattr(dicom, "SequenceOfUltrasoundRegions", None) or [])
        if int(getattr(r, "RegionSpatialFormat", -1)) == 1
    ]
    if not regions:
        return None
    return (
        min(int(r.RegionLocationMinX0) for r in regions),
        min(int(r.RegionLocationMinY0) for r in regions),
        max(int(r.RegionLocationMaxX1) for r in regions) + 1,
        max(int(r.RegionLocationMaxY1) for r in regions) + 1,
    )


def crop_square(frames: np.ndarray, box: Sequence[int], size: int = SIZE) -> np.ndarray:
    """Crop F x H x W x 3 uint8 frames to box, zero-pad to a centred square, resize to size (INTER_AREA)."""
    x0, y0, x1, y1 = box
    sub = frames[:, y0:y1, x0:x1]
    h, w = sub.shape[1:3]
    side = max(h, w)
    square = np.zeros((sub.shape[0], side, side, 3), np.uint8)
    oy, ox = (side - h) // 2, (side - w) // 2
    square[:, oy:oy + h, ox:ox + w] = sub
    return np.stack([cv2.resize(f, (size, size), interpolation=cv2.INTER_AREA) for f in square])


def frame_rate(dicom) -> Optional[float]:
    for keyword in ("RecommendedDisplayFrameRate", "CineRate"):
        value = getattr(dicom, keyword, None)
        if value:
            return float(value)
    frame_time = getattr(dicom, "FrameTime", None)
    return 1000.0 / float(frame_time) if frame_time else None


def clip_starts(n_frames: int, stride: int, clips: int = CLIPS_PER_VIDEO, length: int = CLIP_LEN) -> List[int]:
    last = max(0, n_frames - length * stride)
    return [int(s) for s in np.linspace(0, last, clips).astype(int)]


def normalize(frames: np.ndarray) -> torch.Tensor:
    """T x 224 x 224 x 3 uint8 -> 3 x T x 224 x 224 float, ImageNet normalisation."""
    t = torch.from_numpy(np.ascontiguousarray(frames)).permute(0, 3, 1, 2).float() / 255.0
    return ((t - IMAGENET_MEAN) / IMAGENET_STD).permute(1, 0, 2, 3)


def video_clips(frames: np.ndarray, dicom) -> List[torch.Tensor]:
    """The 4 model clips of one video from its full 8-bit RGB frames (F x H x W x 3)."""
    if frames.ndim == 3:
        frames = np.repeat(frames[..., None], 3, axis=-1)
    box = sector_box(dicom) or (0, 0, frames.shape[2], frames.shape[1])
    square = crop_square(frames, box)
    stride = max(1, int(round((frame_rate(dicom) or 30.0) / 30.0)))
    n = len(square)
    return [
        normalize(square[[min(start + i * stride, n - 1) for i in range(CLIP_LEN)]])
        for start in clip_starts(n, stride)
    ]
