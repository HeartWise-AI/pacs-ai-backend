"""Pixel-level explanation of flagged segments: where the model read each one.

For a flagged segment the model pools a bag of instances, its tracked 320 px crops first and then the full views,
with gated attention (``bag_attention``). Inside a crop it pools the tokens with a softmax over
``q[segment] . token / sqrt(d) + mask_w[segment] * log(mask fraction + 1e-3)``. This module captures the inputs of
``_pool_study`` during the same forward call (no extra encoder pass), takes the crop with the highest bag attention,
recomputes that within-crop softmax exactly, and draws it on the crop at the time slot holding the most attention.

Attention shows where the model read the segment, not a measured lesion: on an external expert set (ARCADE) the
attention peak fell inside the expert stenosis box about half the time.
"""
from __future__ import annotations

import base64
from contextlib import contextmanager

import cv2 as cv
import numpy as np
import torch

PATCH, TUBELET = 16, 2
DISPLAY = 320


@contextmanager
def capture_pooling(model):
    """Record what ``_pool_study`` receives and returns during one forward call."""
    store = {}
    original = getattr(model, "_pool_study", None)
    if original is None:  # not the integrated model: nothing to explain, inference unchanged
        yield store
        return

    def wrapped(tokens, fractions, crop_tokens, crop_fractions, specs):
        out = original(tokens, fractions, crop_tokens, crop_fractions, specs)
        store.update(crop_tokens=crop_tokens, crop_fractions=crop_fractions, specs=list(specs), bag_attention=out[2])
        return out

    model._pool_study = wrapped
    try:
        yield store
    finally:
        del model._pool_study  # back to the class method


def _jpeg(image: np.ndarray) -> str:
    ok, buf = cv.imencode(".jpg", image, [cv.IMWRITE_JPEG_QUALITY, 88])
    if not ok:
        raise ValueError("Cannot encode explanation image")
    return base64.b64encode(buf.tobytes()).decode("ascii")


def _gray(x: np.ndarray) -> np.ndarray:
    return cv.cvtColor((np.clip(x, 0, 1) * 255).astype(np.uint8), cv.COLOR_GRAY2BGR)


def within_crop_attention(model, group_index: int, crop_tokens: torch.Tensor, crop_fractions: torch.Tensor) -> torch.Tensor:
    """The token softmax ``_pool_study`` applies inside one crop, (N,d) tokens -> (N,) weights."""
    q = model.q[group_index]
    logits = (crop_tokens * q[None]).sum(-1) / crop_tokens.shape[-1] ** 0.5
    logits = logits + model.mask_w[group_index] * (crop_fractions + 1e-3).log()
    return logits.float().softmax(-1)


def explain_segments(model, store: dict, clips: list, targets: list[str], crop_res: int) -> list[dict]:
    """One explanation per target segment that has a pass-2 crop; segments without a crop are listed as such."""
    from integrated_model import crop_tensor

    groups = list(model.groups)
    specs, bag = store.get("specs") or [], store.get("bag_attention") or []
    side = crop_res // PATCH
    out = []
    for name in targets:
        gi = groups.index(name)
        chosen = [i for i, s in enumerate(specs) if s.group == gi]
        a = bag[gi].float().cpu() if gi < len(bag) else torch.zeros(0)
        if not chosen or store.get("crop_tokens") is None:
            out.append({"segment": name, "has_crop": False,
                        "max_full_view_attention": round(float(a.max()), 4) if len(a) else None})
            continue
        crop_att, view_att = a[:len(chosen)], a[len(chosen):]
        k = int(crop_att.argmax())
        spec = specs[chosen[k]]
        w = within_crop_attention(model, gi, store["crop_tokens"][chosen[k]], store["crop_fractions"][chosen[k]]).cpu()
        slots = w.numel() // (side * side)
        w = w.reshape(slots, side, side)
        slot = int(w.sum((1, 2)).argmax())
        frame = min(slot * TUBELET, clips[spec.video].shape[0] - 1)
        heat = w[slot].numpy()
        heat = heat / max(float(heat.max()), 1e-12)
        clip = clips[spec.video].float().cpu()
        box = spec.boxes[frame].float().cpu()
        crop = crop_tensor(clip[frame:frame + 1], box[None], DISPLAY)[0].numpy()
        up = cv.resize(heat.astype(np.float32), (DISPLAY, DISPLAY), interpolation=cv.INTER_CUBIC).clip(0, 1)
        colour = cv.applyColorMap((up * 255).astype(np.uint8), cv.COLORMAP_INFERNO)
        alpha = (0.65 * up ** 0.8)[..., None]
        base = _gray(crop)
        overlay = (base * (1 - alpha) + colour * alpha).astype(np.uint8)
        py, px = np.unravel_index(int(np.argmax(heat)), heat.shape)
        cy, cx = int((py + 0.5) * DISPLAY / side), int((px + 0.5) * DISPLAY / side)
        cv.drawMarker(overlay, (cx, cy), (255, 255, 255), cv.MARKER_CROSS, 22, 2)
        cv.drawMarker(overlay, (cx, cy), (40, 40, 40), cv.MARKER_CROSS, 22, 1)
        # Full frame with the crop box and the attention peak, scaled to 320 px on its long side.
        h, wd = clip.shape[-2:]
        s = DISPLAY / max(h, wd)
        full = cv.resize(_gray(clip[frame].numpy()), (int(wd * s), int(h * s)), interpolation=cv.INTER_AREA)
        y, x, bh, bw = box.tolist()
        cv.rectangle(full, (int(x * s), int(y * s)), (int((x + bw) * s), int((y + bh) * s)), (0, 170, 255), 2)
        fy, fx = (y + (py + 0.5) / side * bh) * s, (x + (px + 0.5) / side * bw) * s
        cv.drawMarker(full, (int(fx), int(fy)), (255, 255, 255), cv.MARKER_CROSS, 14, 2)
        top = np.sort(w[slot].flatten().numpy())[::-1]
        out.append({
            "segment": name, "has_crop": True, "video": int(spec.video), "frame": int(frame),
            "crop_box_yxhw": [round(v, 1) for v in box.tolist()],
            "bag_attention": round(float(crop_att[k]), 4),
            "max_full_view_attention": round(float(view_att.max()), 4) if len(view_att) else None,
            "peak_in_crop_xy": [round((px + 0.5) / side, 3), round((py + 0.5) / side, 3)],
            "top10pct_token_share": round(float(top[:max(1, len(top) // 10)].sum() / max(top.sum(), 1e-12)), 3),
            "_images": {"crop": _jpeg(overlay), "frame": _jpeg(full)},
        })
    return out
