import numpy as np
import pydicom


def to_uint8(array: np.ndarray) -> np.ndarray:
    array = array - np.min(array)
    max_value = np.max(array)
    if max_value == 0:
        return np.zeros_like(array, dtype=np.uint8)
    array = array / max_value
    array = array * 255
    return np.uint8(array)


def handle_colorspace(im_array: np.ndarray, dicom_ds: pydicom.Dataset) -> np.ndarray:
    cspace = dicom_ds[(0x0028, 0x0004)].value
    if cspace in ["YBR_FULL", "YBR_FULL_422", "RGB"]:
        return im_array
    if cspace in ["PALETTE COLOR"]:
        return to_uint8(pydicom.pixel_data_handlers.util.apply_color_lut(im_array, dicom_ds))
    if cspace in ["MONOCHROME1", "MONOCHROME2"]:
        recolored = np.expand_dims(im_array, 3)
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
