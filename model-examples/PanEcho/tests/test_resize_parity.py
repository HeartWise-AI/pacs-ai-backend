"""PanEcho 1.1.1 keeps the 1.1.0 (torchvision 0.23) resize: float bilinear with antialias, rounded to uint8."""
import numpy as np
import torch
from torchvision import tv_tensors

from logic import CustomPredictionService, _resize_like_torchvision_0_23


def _video(seed: int = 0) -> tv_tensors.Video:
    rng = np.random.default_rng(seed)
    frames = rng.integers(0, 256, size=(4, 3, 120, 160), dtype=np.uint8)
    return tv_tensors.Video(torch.from_numpy(frames))


def test_resize_is_the_rounded_float_resize_of_torchvision_0_23():
    video = _video()
    expected = torch.nn.functional.interpolate(
        video.float(), size=(256, 256), mode="bilinear", align_corners=False, antialias=True
    ).round().clamp(0, 255).to(torch.uint8)

    resized = _resize_like_torchvision_0_23(video)

    assert isinstance(resized, tv_tensors.Video)
    assert resized.dtype == torch.uint8
    assert torch.equal(resized.as_subclass(torch.Tensor), expected)


def test_transform_output_shape_is_unchanged():
    transform = CustomPredictionService._create_transform(None, "imagenet")
    out = transform(_video(1))
    assert tuple(out.shape) == (4, 3, 224, 224)
    assert out.dtype == torch.float32
