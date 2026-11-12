"""Raw-image and cache datasets for the integrated segmentation model.

ARCADE's supplied train/val/test split is preserved. MHI maps must be supplied
with an explicit split in their manifest; test studies are never training data.
Spatial-token caches are a training speed-up only, not an inference input.
"""
from __future__ import annotations

import hashlib
import json
from pathlib import Path

import cv2
from typing import Sequence
import numpy as np
import torch
from torch.utils.data import Dataset

ARCADE_ROOT = Path("/media/data1/datasets/DeepCoro_CLIP/arcade/syntax")


def sha256_file(path: str | Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(8 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def arcade_records(root: str | Path, split: str):
    if split not in ("train", "val", "test"):
        raise ValueError(f"Unknown ARCADE split {split}")
    root = Path(root)
    with open(root / split / "annotations" / f"{split}.json") as f:
        coco = json.load(f)
    anns = {}
    for a in coco["annotations"]:
        anns.setdefault(a["image_id"], []).append(a)
    return [dict(source="arcade", split=split, sample_id=f"arcade_{split}_{im['id']}",
                 image_path=str(root / split / "images" / im["file_name"]),
                 image_id=im["id"], annotations=anns.get(im["id"], []))
            for im in coco["images"]]


def read_arcade(record: dict, res: int = 224):
    from pycocotools import mask as coco_mask
    image = cv2.imread(record["image_path"], cv2.IMREAD_GRAYSCALE)
    if image is None:
        raise FileNotFoundError(record["image_path"])
    target = np.zeros(image.shape, np.uint8)
    for a in sorted(record["annotations"], key=lambda a: -a["area"]):
        seg = a["segmentation"]
        rle = coco_mask.frPyObjects(seg, *image.shape) if isinstance(seg, list) or isinstance(seg.get("counts"), list) else seg
        if isinstance(rle, list):
            rle = coco_mask.merge(rle)
        region = coco_mask.decode(rle) > 0
        cls = int(a["category_id"])
        target[region] = cls if cls < 26 else 0
    image = cv2.resize(image, (res, res), interpolation=cv2.INTER_AREA)
    target = cv2.resize(target, (res, res), interpolation=cv2.INTER_NEAREST)
    return image, target


def raw_clip_indices(n: int, count: int = 16) -> np.ndarray:
    """Deterministic image-only temporal sampling; no external scout model.

    Uniform sampling spans the video. The old pipeline's ensemble-selected peak
    indices are deliberately not used by integrated deployment/evaluation.
    """
    if n < 1:
        raise ValueError("Empty video")
    return np.rint(np.linspace(0, n - 1, count)).astype(np.int64)


_TOPHAT = None


def dye_score(gray: np.ndarray) -> float:
    """Image-only opacification score: mean black top-hat (dark thin structures) in the central field."""
    global _TOPHAT
    import cv2
    if _TOPHAT is None:
        _TOPHAT = cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (15, 15))
    g = cv2.resize(gray, (256, 256), interpolation=cv2.INTER_AREA)
    return float(cv2.morphologyEx(g, cv2.MORPH_BLACKHAT, _TOPHAT)[25:231, 25:231].mean())


def clip_indices(frames: Sequence[np.ndarray], sampling: str = "uniform16", count: int = 16) -> np.ndarray:
    """uniform16: span the video. peak_s2: 16 frames at stride 2 centred on the smoothed dye-score peak.
    middle_s2: stride 2 centred on the middle frame. Short videos fall back to uniform."""
    n = len(frames)
    if sampling == "uniform16" or n < 2 * count:
        return raw_clip_indices(n, count)
    if sampling == "peak_s2":
        s = np.array([dye_score(f) for f in frames])
        centre = int(np.argmax(np.convolve(s, np.ones(5) / 5, mode="same")))
    elif sampling == "middle_s2":
        centre = (n - 1) / 2
    else:
        raise ValueError(f"Unknown frame sampling {sampling}")
    span = 2 * (count - 1)
    start = int(np.clip(round(centre - span / 2), 0, n - 1 - span))
    return start + 2 * np.arange(count, dtype=np.int64)


def load_video_clip(path: str | Path, return_metadata: bool = False, sampling: str = "uniform16"):
    cap = cv2.VideoCapture(str(path))
    native_fps = float(cap.get(cv2.CAP_PROP_FPS))
    frames = []
    try:
        while True:
            ok, frame = cap.read()
            if not ok:
                break
            frames.append(cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY))
    finally:
        cap.release()
    if not frames:
        raise ValueError(f"Unreadable video {path}")
    idx = clip_indices(frames, sampling)
    clip = torch.from_numpy(np.stack(frames)[idx]).float() / 255
    if return_metadata:
        return clip, {"FileName": str(path), "native_fps": native_fps if np.isfinite(native_fps) and native_fps > 0 else None,
                      "native_frame_count": len(frames), "frame_indices": idx.tolist(),
                      "sampling": "uniform16 across native video; no FPS resampling" if sampling == "uniform16" else f"{sampling}; no FPS resampling",
                      "native_height": frames[0].shape[0], "native_width": frames[0].shape[1]}
    return clip


class CachedMasks(Dataset):
    """One still frame with frozen EJ temporal-mean tokens and pixel labels."""
    def __init__(self, manifest: str | Path, split: str, augment: bool = False,
                 limit: int = 0):
        self.root = Path(manifest).resolve().parent
        with open(manifest) as f:
            self.metadata = json.load(f)
        self.rows = [r for r in self.metadata["samples"] if r["split"] == split]
        if limit:
            self.rows = self.rows[:limit]
        self.augment = augment
        if split != "train" and augment:
            raise ValueError("Augmentation is only allowed for training")
        if not self.rows:
            raise ValueError(f"No {split} samples in {manifest}")

    def __len__(self):
        return len(self.rows)

    def __getitem__(self, i):
        row = self.rows[i]
        path = Path(row["cache"])
        if not path.is_absolute():
            path = self.root / path
        with np.load(path) as z:
            x = torch.from_numpy(z["image"].copy()).float() / 255
            y = torch.from_numpy(z["target"].astype(np.int64))
            tok = torch.from_numpy(z["tokens"].astype(np.float32))
            teacher = torch.from_numpy(z["teacher_prob"].astype(np.float32)) if "teacher_prob" in z else torch.empty(0)
            agreement = torch.from_numpy(z["agreement"].astype(np.float32)) if "agreement" in z else torch.ones_like(x)
        if self.augment:
            # Cached semantic tokens do not transform equivariantly. Apply
            # intensity jitter only, matching the crop fine-tuning baseline.
            x = (x * float(np.random.uniform(0.9, 1.1)) + float(np.random.uniform(-0.03, 0.03))).clamp(0, 1)
        return {"frames": x[None], "target": y[None], "tokens": tok,
                "teacher_prob": teacher[None] if teacher.numel() else teacher,
                "agreement": agreement[None], "sample_id": row["sample_id"]}


class StudyClips(Dataset):
    """Study-level raw clips; labels must already have the patient split checked."""
    def __init__(self, rows, groups, max_views=12, sampling="uniform16"):
        self.rows = rows.reset_index(drop=True)
        self.groups = tuple(groups)
        self.max_views = max_views
        self.sampling = sampling

    def __len__(self):
        return len(self.rows)

    def __getitem__(self, i):
        row = self.rows.iloc[i]
        files = list(row.files)
        # All evaluations use a deterministic filename order. Random sampling
        # may be added by the training caller, without using teacher masses.
        files = sorted(files)[:self.max_views]
        loaded = [load_video_clip(f, return_metadata=True, sampling=self.sampling) for f in files]
        clips = [x[0] for x in loaded]
        severity = torch.tensor([float(row[f"{g}_stenosis"]) / 100 for g in self.groups])
        nan = float("nan")
        procedure = torch.tensor([[float(row[f"{g}_{k}_target"]) if f"{g}_{k}_target" in row else nan for g in self.groups]
                                  for k in ("stent", "timi", "failed")])  # NaN = unknown, not supervised
        lesion = torch.tensor([[float(row[f"{g}_{k}_lesion_target"]) if f"{g}_{k}_lesion_target" in row else nan for g in self.groups]
                               for k in ("calcif", "thrombus", "cto")])  # v11; NaN = not supervised
        pressure = torch.tensor([float(row[k]) if k in row else nan for k in ("ffr_value", "ifr_value")])  # v12; NaN = none
        return {"uid": row.uid, "clips": clips, "severity": severity, "procedure": procedure, "lesion": lesion, "pressure": pressure,
                "patient": str(row.patient_id_anon), "video_provenance": [x[1] for x in loaded]}


def one_study(batch):
    if len(batch) != 1:
        raise ValueError("Integrated study batches must have size one")
    return batch[0]
