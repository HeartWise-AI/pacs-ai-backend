import json
import subprocess
from pathlib import Path

import numpy as np
import torch
from PIL import Image
from torchvision.transforms import InterpolationMode
from torchvision.transforms import functional as TF

N_FRAMES, STRIDE, SIZE, TARGET_FPS = 16, 1, 224, 24
MEAN, STD = [0.5, 0.5, 0.5], [0.5, 0.5, 0.5]
IMAGE_SUFFIXES = {".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp"}


def load_echo(path, n_frames=N_FRAMES, stride=STRIDE, size=SIZE, mean=MEAN, std=STD,
              crop_to_region=None, pad_to_square=None, rotate_180=None, target_fps=TARGET_FPS):
    frames = read_frames(path, n_frames, stride, crop_to_region, pad_to_square, rotate_180, target_fps)
    return to_tensor(frames, size, mean, std)


def read_frames(path, n_frames=N_FRAMES, stride=STRIDE, crop_to_region=None, pad_to_square=None, rotate_180=None,
                target_fps=None):
    def pick(n, fps=None):
        kept = resample_indices(n, fps, target_fps)
        return [kept[i] for i in frame_indices(len(kept), n_frames, stride)]
    path = Path(path)
    if not path.exists():
        raise FileNotFoundError(path)
    kind = file_kind(path)
    is_dicom = kind == "dicom"
    crop = is_dicom if crop_to_region is None else bool(crop_to_region)
    pad = is_dicom if pad_to_square is None else bool(pad_to_square)
    if kind == "dicom":
        frames = _read_dicom(path, pick, crop)
    elif kind == "nifti":
        frames = _read_nifti(path, pick, rotate_180)
    elif kind == "image":
        with Image.open(path) as im:
            frames = [im.convert("RGB").copy() for _ in pick(1)]
    elif kind == "folder":
        frames = _read_folder(path, pick)
    elif kind == "npz":
        frames = _read_npz(path, pick)
    else:
        frames = _read_video(path, pick)
    if pad:
        frames = [_pad_to_square(f) for f in frames]
    return frames


def to_tensor(frames, size=SIZE, mean=MEAN, std=STD):
    out = []
    for f in frames:
        if f.size != (size, size):
            f = TF.resize(f, (size, size), interpolation=InterpolationMode.BICUBIC)
        out.append(TF.to_tensor(f))
    return TF.normalize(torch.stack(out, 0), list(mean), list(std))


def resample_indices(n, src_fps, target_fps):
    if not src_fps or not target_fps or src_fps <= 0:
        return list(range(n))
    t_out = max(1, int(round(n / src_fps * target_fps)))
    return np.clip(np.round(np.arange(t_out) * (src_fps / target_fps)).astype(int), 0, n - 1).tolist()


def frame_indices(n, k=N_FRAMES, stride=STRIDE):
    n = int(n)
    if n <= 0:
        raise ValueError("clip has no frames")
    if k is None:
        return list(range(n))
    k, stride = int(k), max(1, int(stride))
    return [min(i * stride, n - 1) for i in range(k)]


def file_kind(path):
    name = path.name.lower()
    if path.is_dir():
        return "folder"
    if name.endswith(".dcm"):
        return "dicom"
    if name.endswith(".nii") or name.endswith(".nii.gz"):
        return "nifti"
    if path.suffix.lower() in IMAGE_SUFFIXES:
        return "image"
    if name.endswith(".npz"):
        return "npz"
    return "video"


def _read_dicom(path, pick, crop):
    import pydicom
    from pydicom.pixels import iter_pixels

    ds = pydicom.dcmread(str(path), stop_before_pixels=True)
    n = max(1, int(getattr(ds, "NumberOfFrames", 1) or 1))
    box = _ultrasound_region_box(ds) if crop else None
    idx = pick(n, _dicom_fps(ds))
    wanted = sorted({min(i, n - 1) for i in idx})
    decoded = {}
    # iter_pixels returns RGB for YBR_FULL_422 JPEG files; do not convert color again.
    for j, arr in zip(wanted, iter_pixels(str(path), indices=wanted)):
        a = np.asarray(arr).astype(np.uint8)
        img = Image.fromarray(a).convert("RGB") if a.ndim == 2 else Image.fromarray(a)
        decoded[j] = img.crop(box) if box is not None else img
    if not decoded:
        raise RuntimeError(f"decoded zero frames from {path}")
    return [decoded[min(i, n - 1)].copy() for i in idx]


def _dicom_fps(ds):
    for tag, f in (("FrameTime", lambda v: 1000.0 / v), ("CineRate", float), ("RecommendedDisplayFrameRate", float)):
        try:
            v = float(getattr(ds, tag))
            if v > 0:
                return f(v)
        except (AttributeError, TypeError, ValueError):
            continue
    return None


def _ultrasound_region_box(ds):
    regions = getattr(ds, "SequenceOfUltrasoundRegions", None)
    if not regions:
        return None
    boxes = []
    for r in regions:
        try:
            boxes.append((int(r.RegionLocationMinX0), int(r.RegionLocationMinY0),
                          int(r.RegionLocationMaxX1), int(r.RegionLocationMaxY1)))
        except Exception:
            continue
    if not boxes:
        return None
    x0, y0 = min(b[0] for b in boxes), min(b[1] for b in boxes)
    x1, y1 = max(b[2] for b in boxes), max(b[3] for b in boxes)
    return (x0, y0, x1, y1) if x1 > x0 and y1 > y0 else None


def _read_nifti(path, pick, rotate_180):
    import nibabel as nib

    img = nib.load(str(path))
    data, shape = img.dataobj, img.shape
    # Echo NIfTI is (x, y[, t][, c]); color CardiacNet videos are (x, y, t, 3).
    is_rgb = len(shape) == 4 and int(shape[-1]) in (3, 4)
    n = int(shape[2]) if is_rgb else (int(shape[-1]) if len(shape) >= 3 else 1)
    n = max(1, n)
    is_ycbcr = is_rgb and _nifti_is_ycbcr(data)
    if rotate_180 is None:
        p = str(path).lower()
        rotate_180 = "regional_wall_motion_abnormality" in p or ("cardiacnet" in p and not is_rgb)
    idx = pick(n)
    decoded = {}
    for j in sorted({min(i, n - 1) for i in idx}):
        if is_rgb:
            f = np.asarray(data[:, :, j, :3], dtype=np.uint8)
            if is_ycbcr:
                f = np.asarray(Image.fromarray(f, mode="YCbCr").convert("RGB"))
            f = f.astype(np.float32)
        else:
            f = np.asarray(data[..., j] if len(shape) >= 3 else data, dtype=np.float32)
            f = np.squeeze(f)
            if f.ndim > 2:
                f = f[..., f.shape[-1] // 2]
            if f.ndim == 1:
                f = f[:, None]
        f = np.swapaxes(f, 0, 1)
        if rotate_180:
            f = f[::-1, ::-1]
        f = np.ascontiguousarray(f)
        lo, hi = float(f.min()), float(f.max())
        f = (f - lo) / (hi - lo) if hi > lo else f * 0.0
        decoded[j] = Image.fromarray((f * 255.0).astype(np.uint8)).convert("RGB")
    return [decoded[min(i, n - 1)].copy() for i in idx]


def _nifti_is_ycbcr(data):
    first = np.asarray(data[:, :, 0, :3], dtype=np.float32)
    y, cb, cr = (float(first[..., c].mean()) for c in range(3))
    return abs(cb - 128.0) < 4.0 and abs(cr - 128.0) < 4.0 and abs(y - cb) > 20.0


def _read_folder(path, pick):
    files = [p for p in path.iterdir() if p.suffix.lower() in IMAGE_SUFFIXES]
    numeric = all(p.stem.isdigit() for p in files)
    files.sort(key=(lambda p: int(p.stem)) if numeric else (lambda p: p.name))
    if not files:
        raise RuntimeError(f"no frame images in {path}")
    idx = pick(len(files))
    decoded = {}
    for j in sorted(set(idx)):
        with Image.open(files[j]) as im:
            decoded[j] = im.convert("RGB").copy()
    return [decoded[i].copy() for i in idx]


def _read_npz(path, pick):
    with np.load(path, allow_pickle=False) as z:
        frames = z["frames"]
    return [Image.fromarray(np.ascontiguousarray(frames[j])).convert("RGB") for j in pick(len(frames))]


def _read_video(path, pick):
    width, height, n = probe_video(path)
    if n <= 0:
        n = count_frames(path, width, height)
    idx = pick(n, video_fps(path))
    raw = _ffmpeg_rgb(path, max(idx) + 1)
    size = width * height * 3
    available = len(raw) // size
    if available <= 0:
        raise RuntimeError(f"decoded zero frames from {path}")
    out = []
    for i in idx:
        j = min(i, available - 1)
        a = np.frombuffer(raw[j * size:(j + 1) * size], dtype=np.uint8).reshape(height, width, 3)
        out.append(Image.fromarray(a))
    return out


def probe_video(path):
    cmd = ["ffprobe", "-v", "error", "-select_streams", "v:0",
           "-show_entries", "stream=width,height,nb_frames", "-of", "json", str(path)]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"ffprobe failed for {path}: {proc.stderr.strip()}")
    streams = json.loads(proc.stdout or "{}").get("streams") or []
    if not streams:
        raise RuntimeError(f"no video stream in {path}")
    s = streams[0]
    nb = s.get("nb_frames")
    n = int(nb) if nb not in (None, "N/A") and str(nb).isdigit() else 0
    return int(s["width"]), int(s["height"]), n


def video_fps(path):
    cmd = ["ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=avg_frame_rate,r_frame_rate",
           "-of", "json", str(path)]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    s = (json.loads(proc.stdout or "{}").get("streams") or [{}])[0]
    for key in ("avg_frame_rate", "r_frame_rate"):
        num, _, den = str(s.get(key, "0/0")).partition("/")
        try:
            fps = float(num) / float(den or 1)
        except (ValueError, ZeroDivisionError):
            continue
        if fps > 0:
            return fps
    return None


def _ffmpeg_rgb(path, n_frames=None):
    cmd = ["ffmpeg", "-nostdin", "-threads", "1", "-v", "error", "-i", str(path),
           "-f", "rawvideo", "-pix_fmt", "rgb24"]
    if n_frames:
        cmd += ["-vframes", str(n_frames)]
    proc = subprocess.run(cmd + ["-"], capture_output=True)
    if proc.returncode != 0:
        raise RuntimeError(f"ffmpeg failed for {path}: {proc.stderr.decode(errors='ignore').strip()}")
    return proc.stdout


def count_frames(path, width, height):
    return len(_ffmpeg_rgb(path)) // (width * height * 3)


def _pad_to_square(img):
    w, h = img.size
    if w == h:
        return img
    side = max(w, h)
    out = Image.new("RGB", (side, side), (0, 0, 0))
    out.paste(img.convert("RGB"), ((side - w) // 2, (side - h) // 2))
    return out
