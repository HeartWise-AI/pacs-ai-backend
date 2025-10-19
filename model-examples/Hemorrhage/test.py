from nnunetv2.paths import nnUNet_raw
from batchgenerators.utilities.file_and_folder_operations import join
from nnunetv2.imageio.simpleitk_reader_writer import SimpleITKIO
from nnunetv2.inference.predict_from_raw_data import nnUNetPredictor
import torch
import os
import numpy as np

if __name__ == '__main__':
    workingDirectory = os.getcwd() + '/models'
    model_dir = 'Dataset005_WinMultiICHv5'
    class_mapping_filepath = os.path.join(workingDirectory, model_dir, 'class_mapping.json')
    model_folder = os.path.join(workingDirectory, model_dir, 'nnUNetTrainer__nnUNetPlans__3d_fullres')
    use_folds = 0,
    print("Initializing model")
    predictor = nnUNetPredictor(
        tile_step_size=0.5,
        use_gaussian=True,
        use_mirroring=True,
        perform_everything_on_device=True,
        device=torch.device('cuda', 0),
        verbose=False,
        verbose_preprocessing=False,
        allow_tqdm=True)
    print("Loading model weights")
    predictor.initialize_from_trained_model_folder(
        model_folder,
        use_folds=use_folds,
        checkpoint_name='checkpoint_final.pth')
    img, props = SimpleITKIO().read_images([('ct.nii.gz')])
    img_data = np.clip(img, -10, 140)
    ret = predictor.predict_from_list_of_npy_arrays([img_data], None, [props], None, 2, save_probabilities=False, num_processes_segmentation_export=2)
    print(ret)