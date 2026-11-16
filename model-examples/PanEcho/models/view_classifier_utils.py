import numpy as np
import pydicom

from utils.dicom_pixels import to_display_uint8


def to_uint8(array: np.ndarray) -> np.ndarray:
    array = array - np.min(array)
    max_value = np.max(array)
    if max_value == 0:
        return np.zeros_like(array, dtype=np.uint8)
    array = array / max_value
    array = array * 255
    return np.uint8(array)


JPEG_LS_TRANSFER_SYNTAXES = {"1.2.840.10008.1.2.4.80", "1.2.840.10008.1.2.4.81"}
_PREFERRED_JPEG_LS_PLUGIN = "pyjpegls"


def _retag_mislabeled_ybr(dcm: pydicom.Dataset) -> None:
    """Correct mislabeled YBR_FULL_422 before decoding.

    Some echo vendors tag PhotometricInterpretation as YBR_FULL_422 while the
    native PixelData buffer is full YBR_FULL/RGB sized (1/3 larger). Retag
    for that case; leave encapsulated payloads to the decoder plugin.
    """
    pi = getattr(dcm, "PhotometricInterpretation", None)
    if pi == "YBR_FULL_422" and hasattr(dcm, "PixelData"):
        rows = int(dcm.Rows)
        cols = int(dcm.Columns)
        frames = int(getattr(dcm, "NumberOfFrames", 1) or 1)
        samples = int(getattr(dcm, "SamplesPerPixel", 3) or 3)
        expected_full = rows * cols * frames * samples
        try:
            if len(dcm.PixelData) >= expected_full:
                dcm.PhotometricInterpretation = "YBR_FULL"
        except TypeError:
            pass


def decoding_plugin(dcm: pydicom.Dataset) -> str:
    """Name of the decoder plugin to request, or "" for pydicom's default order.

    JPEG-LS frames decode about five times faster with the CharLS plugin (pyjpegls) than with
    pylibjpeg and give identical pixels (the codec is lossless), so it is preferred when
    installed.
    """
    try:
        syntax = str(dcm.file_meta.TransferSyntaxUID)
        if syntax in JPEG_LS_TRANSFER_SYNTAXES:
            from pydicom.pixels import get_decoder

            if _PREFERRED_JPEG_LS_PLUGIN in get_decoder(syntax).available_plugins:
                return _PREFERRED_JPEG_LS_PLUGIN
    except Exception:
        pass
    return ""


def load_pixel_array(dcm: pydicom.Dataset) -> np.ndarray:
    """Decode all DICOM pixels, correcting mislabeled YBR_FULL_422 when needed."""
    _retag_mislabeled_ybr(dcm)
    plugin = decoding_plugin(dcm)
    if plugin:
        try:
            dcm.pixel_array_options(decoding_plugin=plugin)
            return dcm.pixel_array
        except Exception:
            dcm.pixel_array_options()
    return dcm.pixel_array


def clip_window(frame_count: int, clip_len: int) -> tuple[int, int]:
    """Frames ``[start, stop)`` PanEcho samples from a clip: the centred ``clip_len`` frames."""
    start = 0 if frame_count < clip_len else (frame_count - clip_len) // 2
    return start, min(start + clip_len, frame_count)


def load_frame_window(dcm: pydicom.Dataset, start: int, stop: int) -> np.ndarray | None:
    """Decode only frames ``[start, stop)``; ``None`` when the caller must decode everything.

    Returns exactly ``load_pixel_array(dcm)[start:stop]`` for 8-bit multi-frame data, where no
    later step depends on statistics of the whole clip. Other data (high-bit input is windowed
    by clip-level percentiles) and any decoding problem return ``None`` so the caller keeps the
    whole-clip path and its error handling.
    """
    try:
        if int(getattr(dcm, "BitsAllocated", 0) or 0) != 8:
            return None
        frame_count = int(getattr(dcm, "NumberOfFrames", 1) or 1)
        if frame_count <= 1 or not 0 <= start < stop <= frame_count:
            return None
        from pydicom.pixels import iter_pixels

        _retag_mislabeled_ybr(dcm)
        frames = list(
            iter_pixels(dcm, indices=range(start, stop), decoding_plugin=decoding_plugin(dcm))
        )
        if len(frames) != stop - start or any(frame.dtype != np.uint8 for frame in frames):
            return None
        return np.stack(frames)
    except Exception:
        return None


def handle_colorspace(im_array: np.ndarray, dicom_ds: pydicom.Dataset) -> np.ndarray:
    cspace = dicom_ds[(0x0028, 0x0004)].value
    if cspace in ["YBR_FULL", "YBR_FULL_422", "RGB"]:
        return im_array
    if cspace in ["PALETTE COLOR"]:
        return to_uint8(pydicom.pixel_data_handlers.util.apply_color_lut(im_array, dicom_ds))
    if cspace in ["MONOCHROME1", "MONOCHROME2"]:
        recolored = np.expand_dims(to_display_uint8(im_array, cspace), 3)
        return np.repeat(recolored, 3, 3)
    return im_array


def mask_and_crop(movie: np.ndarray) -> np.ndarray | str:
    try:
        from skimage import morphology

        sum_channel_mov = np.sum(movie, axis=3)
        diff_mov = np.diff(sum_channel_mov, axis=0)
        if abs(np.sum(diff_mov, dtype=int)) == 0:
            return "failed to detect motion"

        mask = np.sum(diff_mov.astype(bool), axis=0) > 5

        selem = morphology.disk(5)
        eroded = morphology.erosion(mask, selem)
        dilated = morphology.dilation(eroded, selem)

        mask_3channel = np.zeros([dilated.shape[0], dilated.shape[1], 3])
        mask_3channel[:, :, 0] = dilated
        mask_3channel[:, :, 1] = dilated
        mask_3channel[:, :, 2] = dilated
        mask_3channel = mask_3channel.astype(bool)

        x_locations = np.max(dilated, axis=0)
        y_locations = np.max(dilated, axis=1)
        left = np.where(x_locations)[0][0]
        right = np.where(x_locations)[0][-1]
        top = np.where(y_locations)[0][0]
        bottom = np.where(y_locations)[0][-1]
        h = bottom - top
        w = right - left

        pad = int(max([h, w]) / 2)
        x_center = right - int(w / 2) + pad
        y_center = bottom - int(h / 2) + pad

        size = int(max([h, w]) / 2) * 2
        crop_left = int(x_center - (size / 2))
        crop_right = int(x_center + (size / 2))
        crop_top = int(y_center - (size / 2))
        crop_bottom = int(y_center + (size / 2))

        out_movie = np.zeros([movie.shape[0], size, size, movie.shape[3]], dtype="uint8")
        for frame in range(movie.shape[0]):
            masked_frame = movie[frame, :, :] * mask_3channel
            padded_frame = np.pad(
                masked_frame,
                ((pad, pad), (pad, pad), (0, 0)),
                mode="constant",
                constant_values=0,
            )
            out_movie[frame, :, :, :] = padded_frame[
                crop_top:crop_bottom, crop_left:crop_right, :
            ]
        return out_movie
    except Exception:
        return "failed to detect motion"
