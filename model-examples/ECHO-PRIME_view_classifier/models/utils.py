import pydicom
import numpy as np

def handle_colorspace(im_array: np.ndarray, dicom_ds: pydicom.Dataset) -> np.ndarray:
    """
    Handle colorspace conversion for DICOM images.
    Args:
        im_array: numpy array of the image pixel array
        dicom_ds: pydicom Dataset object
    Returns:
        numpy array of the image in RGB format
    """
    cspace = dicom_ds[(0x0028, 0x0004)].value
    if cspace in ['YBR_FULL', 'YBR_FULL_422']:
        recolored = pydicom.pixel_data_handlers.convert_color_space(im_array, cspace, 'RGB')
    elif cspace in ['RGB']:
        recolored = pydicom.pixel_data_handlers.convert_color_space(im_array, cspace, cspace)
    elif cspace in ['PALETTE COLOR']:
        recolored = to_uint8(pydicom.pixel_data_handlers.util.apply_color_lut(im_array, dicom_ds))
    elif cspace in ['MONOCHROME1', 'MONOCHROME2']:
        recolored = np.expand_dims(im_array, 3)
        recolored = np.repeat(recolored, 3, 3)
    else:
        recolored = im_array
    return recolored


def mask_and_crop(movie: np.ndarray) -> np.ndarray:
    """
    Mask and crop the ultrasound cone from the image.
    Args:
        movie: numpy array of the image pixel array
    Returns:
        numpy array of the image in RGB format
    """
    try:
        sum_channel_mov = np.sum(movie, axis=3)
        diff_mov = np.diff(sum_channel_mov,axis=0)
        if abs(np.sum(diff_mov, dtype=int)) > 0:
            mask = np.sum(diff_mov.astype(bool), axis=0) > 5

            # erosion, followed by dilation to remove ecg traces touching cone
            selem = morphology.disk(5)
            eroded = morphology.erosion(mask, selem)
            dilated = morphology.dilation(eroded, selem)

            # make mask 3-channel for more vectorized multiplication
            mask_3channel = np.zeros([dilated.shape[0], dilated.shape[1], 3])
            mask_3channel[:,:,0] = dilated
            mask_3channel[:,:,1] = dilated
            mask_3channel[:,:,2] = dilated
            mask_3channel = mask_3channel.astype(bool)

            # get size of cropped movie
            x_locations = np.max(dilated, axis=0)
            y_locations = np.max(dilated, axis=1)
            left = np.where(x_locations)[0][0]
            right = np.where(x_locations)[0][-1]
            top = np.where(y_locations)[0][0]
            bottom = np.where(y_locations)[0][-1]
            h = bottom - top
            w = right - left

            # padding length for frame in x and y in case crop is beyond image boundaries
            pad = int(max([h,w])/2)
            x_center = right - int(w/2) + pad
            y_center = bottom - int(h/2) + pad

            # height and width of new frames
            size = int(max([h,w])/2)*2
            crop_left = int(x_center - (size/2))
            crop_right = int(x_center + (size/2))
            crop_top = int(y_center - (size/2))
            crop_bottom = int(y_center + (size/2))

            # multiply each frame by mask, pad, and center crop
            out_movie = np.zeros([movie.shape[0],size,size,movie.shape[3]], dtype='uint8')
            for frame in range(movie.shape[0]):
                masked_frame = movie[frame,:,:] * mask_3channel
                padded_frame = np.pad(masked_frame, ((pad,pad), (pad,pad), (0,0)), mode='constant', constant_values=0)
                out_movie[frame, :, :, :] = padded_frame[crop_top:crop_bottom, crop_left:crop_right, :]
            return(out_movie)
        else:
            return('failed to detect motion')
    except:
        return('failed to detect motion')