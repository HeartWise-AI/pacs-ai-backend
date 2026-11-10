"""Keep only diagnostic left/right coronary contrast runs, the inputs DeepCoro-CLIP was trained on.

Each uploaded run is classified by VasoVision (heartwise/VasoVision, X3D-M), the procedure/view classifier that
model-examples/DeepCoro_CLIP/procedure_view_classifier serves, with the same preprocessing. A run is kept when:
  main_structure in {Left Coronary, Right Coronary}, contrast_agent == yes, stent_presence == absent, and its status
  is "diagnostic" (not PCI, and not a post-PCI run, i.e. taken after a stented run of the same artery).
The status rule is procedure_view_classifier's `_assign_procedure_status`, in SeriesTime order.
"""
from __future__ import annotations

import json
import os
import uuid
from pathlib import Path

import cv2 as cv
import numpy as np
import pydicom
import torch
import torch.nn as nn
from torch.nn import functional as F
from torchvision.transforms import v2

KEEP_STRUCTURES = ("Left Coronary", "Right Coronary")


class _Head(nn.Module):
    def __init__(self, dim_in: int, num_classes: int):
        super().__init__()
        self.fc1 = nn.Conv3d(dim_in, 2048, kernel_size=1)
        self.regress = nn.Linear(2048, num_classes)

    def forward(self, x):
        return self.regress(F.relu(self.fc1(x)).mean([2, 3, 4]))


class _MultiHead(nn.Module):
    def __init__(self, dim_in: int, head_structure: dict[str, int]):
        super().__init__()
        self.heads = nn.ModuleDict({k: nn.ModuleList([_Head(dim_in, n)]) for k, n in head_structure.items()})

    def forward(self, x):
        return {k: h[0](x) for k, h in self.heads.items()}


class RunFilter:
    def __init__(self, directory: str | Path, device: str):
        from pytorchvideo.models.hub import x3d_m  # architecture only; weights come from the pinned checkpoint

        directory = Path(directory)
        self.cfg = json.loads((directory / "config.json").read_text())
        mapping = json.loads((directory / self.cfg["class_mapping"]).read_text())
        self.labels = {head: {v: k for k, v in classes.items()} for head, classes in mapping.items()}
        model = x3d_m(pretrained=False)
        model.blocks[-1] = _MultiHead(192, self.cfg["head_structure"])
        state = torch.load(directory / self.cfg["model_path"], map_location="cpu", weights_only=False)["model_state_dict"]
        torch.nn.modules.utils.consume_prefix_in_state_dict_if_present(state, "module.")
        model.load_state_dict(state)
        self.model = model.to(device).eval()
        self.device = device

    def _video(self, dicom: pydicom.Dataset) -> torch.Tensor | None:
        """procedure_view_classifier preprocessing: MJPG round trip at 15 fps, resize, normalise, 72 frames, stride 2."""
        px = dicom.pixel_array
        if px.ndim != 3:
            return None
        rgb = np.repeat(px[..., None], 3, axis=-1)
        path = f"tmp_{uuid.uuid4()}.avi"
        out = cv.VideoWriter(path, cv.VideoWriter_fourcc(*"MJPG"), 15, rgb.shape[1:3])
        try:
            for frame in rgb:
                out.write(frame)
            out.release()
            cap = cv.VideoCapture(path)
            frames = []
            while True:
                ok, frame = cap.read()
                if not ok:
                    break
                frames.append(cv.cvtColor(frame, cv.COLOR_BGR2RGB))
            cap.release()
        finally:
            if os.path.exists(path):
                os.remove(path)
        if not frames:
            return None
        video = torch.from_numpy(np.asarray(frames).transpose(0, 3, 1, 2).astype(np.float32))
        video = v2.Resize((self.cfg["resize"], self.cfg["resize"]), antialias=None)(video)
        video = v2.Normalize(self.cfg["mean"], self.cfg["std"])(video).permute(1, 0, 2, 3)  # C,T,H,W
        n = self.cfg["frames"]
        if video.shape[1] < 2 * n:
            video = torch.cat([video, video.new_zeros(video.shape[0], 2 * n - video.shape[1], *video.shape[2:])], 1)
        return video[:, 2 * torch.arange(n)]

    @torch.no_grad()
    def classify(self, dicoms: list[pydicom.Dataset]) -> list[dict | None]:
        out: list[dict | None] = []
        for dicom in dicoms:
            video = self._video(dicom)
            if video is None:
                out.append(None)
                continue
            logits = self.model(video[None].to(self.device))
            row = {}
            for head, n in self.cfg["head_structure"].items():
                if n == 1:
                    row[head] = self.labels[head][int(torch.sigmoid(logits[head])[0, 0] >= 0.5)]
                else:
                    row[head] = self.labels[head][int(logits[head][0].argmax())]
            out.append(row)
        return out


def series_time(dicom: pydicom.Dataset) -> float:
    try:
        return float(str(dicom[(0x0008, 0x0031)].value))
    except Exception:
        return float("inf")


def assign_status(predictions: list[dict], times: list[float]) -> list[str]:
    """PCI = stented run; POST_PCI = contrast run of an artery after a stented run of that artery; else diagnostic."""
    status = ["diagnostic"] * len(predictions)
    seen_pci: set[str] = set()
    for i in sorted(range(len(predictions)), key=lambda j: times[j]):
        p = predictions[i]
        if p["stent_presence"] == "present":
            status[i] = "PCI"
        elif p["main_structure"] in seen_pci and p["contrast_agent"] == "yes":
            status[i] = "POST_PCI"
        if p["stent_presence"] == "present":
            seen_pci.add(p["main_structure"])
    return status


def keep_mask(predictions: list[dict | None], times: list[float]) -> list[bool]:
    idx = [i for i, p in enumerate(predictions) if p is not None]
    status = assign_status([predictions[i] for i in idx], [times[i] for i in idx])
    keep = [False] * len(predictions)
    for i, s in zip(idx, status):
        p = predictions[i]
        keep[i] = (p["main_structure"] in KEEP_STRUCTURES and p["contrast_agent"] == "yes"
                   and p["stent_presence"] == "absent" and s == "diagnostic")
    return keep
