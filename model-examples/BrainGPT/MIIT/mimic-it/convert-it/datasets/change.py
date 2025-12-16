import os
import json

from abstract_dataset import AbstractDataset
from tqdm import tqdm
from glob import glob
from concurrent.futures import ThreadPoolExecutor
from image_utils import create_folder

class SpotTheDifference_eval(AbstractDataset): # "short_name" should be specified, and the last "for loop" should be modified for your usage
    def __init__(
        self,
        name: str = "SpotTheDifference",
        short_name="eval",
        *,
        image_path: str,
        num_threads: int,
    ):
        """
        Initializes a SpotTheDifference dataset.

        Args:
            name (str): The name of the dataset. Defaults to "SpotTheDifference".
            short_name (str): The short name of the dataset. Defaults to "SD".
            image_path (str): The path containing the dataset images, downloaded from
                https://drive.google.com/file/d/1OVb4_3Uec_xbyUk90aWC6LFpKsIOtR7v/view?usp=sharing.
            num_threads (int): The number of threads to use for processing the images.
        """
        super().__init__(name, short_name, image_path, num_threads)

    def _load_images(self, image_path: str, num_thread: int) -> dict[str, bytes]:
        """
        Loads the images from the dataset.

        Args:
            image_path (str): The path to the directory containing the dataset images.
            num_threads (int): The number of threads to use for processing the images.

        Returns:
            dict[str, bytes]: A dictionary where the keys are image identifiers and the values are image bytes.
        """
        file_names = glob(os.path.join(image_path, "*"))
        names = set()
        for file_name in file_names:
            # file_name: fullpath
            # print(file_name)
            image_name = file_name.split("/")[-1].split(".")[0]
            # print(image_name)
            id = image_name #image_name: rel_path - .bmp
            names.add(id)
        ids = list(sorted(list(names)))
        # print(ids)

        bmps_path = glob(os.path.join(image_path, "*.bmp"))
        jpgs_path = glob(os.path.join(image_path, "*.jpg"))
        jpegs_path = glob(os.path.join(image_path, "*.jpeg"))
        pngs_path = glob(os.path.join(image_path, "*.png"))
        jpgs = set()
        bmps = set()
        pngs = set()
        jpegs = set()
        for path in bmps_path:
            bmps.add(path.split("/")[-1].split(".")[0]) #bmps: rel_path - .bmp
        for path in jpgs_path:
            jpgs.add(path.split("/")[-1].split(".")[0])
        for path in pngs_path:
            pngs.add(path.split("/")[-1].split(".")[0])
        for path in jpegs_path:
            jpegs.add(path.split("/")[-1].split(".")[0])
        
        # print(bmps)

        def get_path(file_name):
            # print(file_name)
            if file_name in jpgs:
                return os.path.join(image_path, file_name + ".jpg")
            elif file_name in pngs:
                # print("file_name", file_name, os.path.join(image_path, file_name + ".png"))
                return os.path.join(image_path, file_name + ".png")
            elif file_name in jpegs:
                return os.path.join(image_path, file_name + ".jpeg")
            elif file_name in bmps:
                return os.path.join(image_path, file_name + ".bmp")

            else:
                # print("===================================", file_name)
                raise Exception("File not found")

        def read_image(file_name) -> bytes:
            with open(file_name, "rb") as f:
                return f.read()

        file_not_found = []

        images = {}
        for id in tqdm(ids, desc="Reading images"):
            i = int(id.split("_")[1])
            if i >7751:
                try:
                    file_1 = get_path(id)
                    # file_2 = get_path(id + "_2")
                    # print(file_1, file_2)
                    images[id.zfill(5)] = read_image(file_1)
                    # images[id.zfill(5) + "_2"] = read_image(file_2)
                except Exception as e:
                    file_not_found.append(id)
                    # print(f"File not found: {id}")
                    # print(f"Error: {e}")

        create_folder("log")
        with open("log/file_not_found.log", "w") as f:
            json.dump(file_not_found, f, indent=4)

        return images
    
class SpotTheDifference(AbstractDataset): # "short_name" should be specified, and the last "for loop" should be modified for your usage
    def __init__(
        self,
        name: str = "SpotTheDifference",
        short_name="PACS_AI",
        *,
        image_path: str,
        num_threads: int,
    ):
        """
        Initializes a SpotTheDifference dataset.

        Args:
            name (str): The name of the dataset. Defaults to "SpotTheDifference".
            short_name (str): The short name of the dataset. Defaults to "SD".
            image_path (str): The path containing the dataset images, downloaded from
                https://drive.google.com/file/d/1OVb4_3Uec_xbyUk90aWC6LFpKsIOtR7v/view?usp=sharing.
            num_threads (int): The number of threads to use for processing the images.
        """
        super().__init__(name, short_name, image_path, num_threads)

    # def _load_images(self, image_path: str, num_thread: int) -> dict[str, bytes]:
    #     """
    #     Loads the images from the dataset.

    #     Args:
    #         image_path (str): The path to the directory containing the dataset images.
    #         num_threads (int): The number of threads to use for processing the images.

    #     Returns:
    #         dict[str, bytes]: A dictionary where the keys are image identifiers and the values are image bytes.
    #     """
    #     file_names = glob(os.path.join(image_path, "*"))
    #     names = set()
    #     for file_name in file_names:
    #         # file_name: fullpath
    #         print(f" on a des noms  {file_name}")
    #         image_name = file_name.split("/")[-1].split(".")[0]
    #         print(f"on a maintenant des {image_name}")
    #         id = image_name #image_name: rel_path - .bmp
    #         names.add(id)
    #     ids = list(sorted(list(names)))
    #     # print(ids)

    #     bmps_path = glob(os.path.join(image_path, "*.bmp"))
    #     jpgs_path = glob(os.path.join(image_path, "*.jpg"))
    #     print(f"on a trouvé : {len(jpgs_path)} jpgs")
    #     jpegs_path = glob(os.path.join(image_path, "*.jpeg"))
    #     pngs_path = glob(os.path.join(image_path, "*.png"))
    #     jpgs = set()
    #     bmps = set()
    #     pngs = set()
    #     jpegs = set()
    #     for path in bmps_path:
    #         bmps.add(path.split("/")[-1].split(".")[0]) #bmps: rel_path - .bmp
    #     for path in jpgs_path:
    #         jpgs.add(path.split("/")[-1].split(".")[0])
    #     for path in pngs_path:
    #         pngs.add(path.split("/")[-1].split(".")[0])
    #     for path in jpegs_path:
    #         jpegs.add(path.split("/")[-1].split(".")[0])
        
    #     # print(bmps)

    #     def get_path(file_name):
    #         # print(file_name)
    #         if file_name in jpgs:
    #             return os.path.join(image_path, file_name + ".jpg")
    #         elif file_name in pngs:
    #             # print("file_name", file_name, os.path.join(image_path, file_name + ".png"))
    #             return os.path.join(image_path, file_name + ".png")
    #         elif file_name in jpegs:
    #             return os.path.join(image_path, file_name + ".jpeg")
    #         elif file_name in bmps:
    #             return os.path.join(image_path, file_name + ".bmp")

    #         else:
    #             # print("===================================", file_name)
    #             raise Exception("File not found")

    #     def read_image(file_name) -> bytes:
    #         with open(file_name, "rb") as f:
    #             return f.read()

    #     file_not_found = []

    #     images = {}
    #     for id in tqdm(ids, desc="Reading images"):
    #         # i = int(id.split("_")[1])
    #         # if i < 7752:
    #             try:
    #                 file_1 = get_path(id)
    #                 # file_2 = get_path(id + "_2")
    #                 # print(file_1, file_2)
    #                 images[id.zfill(5)] = read_image(file_1)
    #                 # images[id.zfill(5) + "_2"] = read_image(file_2)
    #             except Exception as e:
    #                 file_not_found.append(id)
    #                 # print(f"File not found: {id}")
    #                 # print(f"Error: {e}")

    #     create_folder("log")
    #     with open("log/file_not_found.log", "w") as f:
    #         json.dump(file_not_found, f, indent=4)

    #     return images
    
    # Version perrso parce que l'autre est trop restrictive 
    def _load_images(self, image_path: str, num_thread: int) -> dict[str, bytes]:
        """
        Charge les images et formate l'ID pour correspondre au template JSON :
        Adri_IMG_ID_XXXX_slice_sample_XX
        """
        # 1. Récupération de tous les fichiers images
        extensions = ["*.png", "*.jpg", "*.jpeg", "*.bmp"]
        all_files = []
        for ext in extensions:
            all_files.extend(glob(os.path.join(image_path, ext)))
        
        
        if not all_files:
             all_files = [os.path.join(image_path, f) for f in os.listdir(image_path) 
                          if f.lower().endswith(('.png', '.jpg', '.jpeg', '.bmp'))]

        print(f"Fichiers trouvés : {len(all_files)}")
        
        images = {}
        file_not_found = []

        for file_path in tqdm(all_files, desc="Reading images"):
            try:
                filename = os.path.basename(file_path)
                
                # ÉTAPE CLÉ : Nettoyage du nom pour correspondre à  JSON
                
                # 1. On enlève l'extension finale (.png)
                # Ex: ID_...79.nii_slice_sample_01.png -> ID_...79.nii_slice_sample_01
                name_without_ext = os.path.splitext(filename)[0]
                
                # 2. On enlève le ".nii" s'il est présent et on remplace les points restants par rien ou _
                # Cela permet d'avoir un ID propre sans points au milieu
                clean_name = name_without_ext.replace(".nii", "").replace(".", "_")
             
                full_id = clean_name
                
                # Résultat final : Adri_IMG_ID_1423bd41_ID_72db71a879_slice_sample_01
                
                with open(file_path, "rb") as f:
                    images[full_id] = f.read()
                    
            except Exception as e:
                print(f"Erreur sur {file_path}: {e}")
                file_not_found.append(file_path)

        if file_not_found:
            create_folder("log")
            with open("log/file_not_found.log", "w") as f:
                json.dump(file_not_found, f, indent=4)

        return images

class CocoGeneralDifference(AbstractDataset):
    def __init__(
        self,
        name: str = "CocoGeneralDifference",
        short_name="CGD",
        *,
        image_path: str,
        num_threads: int,
    ):
        """
        Initializes a CocoGeneralDifference dataset.

        Args:
            name (str): The name of the dataset. Defaults to "CocoGeneralDifference".
            short_name (str): The short name of the dataset. Defaults to "CGD".
            image_path (str): The path containing the dataset images, downloaded from
                http://images.cocodataset.org/zips/train2017.zip.
            num_threads (int): The number of threads to use for processing the images.
        """
        super().__init__(name, short_name, image_path, num_threads)

    def _load_images(self, image_path: str, num_thread: int) -> dict[str, bytes]:
        """
        Loads the images from the dataset.

        Args:
            image_path (str): The path to the directory containing the dataset images.
            num_threads (int): The number of threads to use for processing the images.

        Returns:
            dict[str, bytes]: A dictionary where the keys are image identifiers and the values are image bytes.
        """
        file_names = glob(os.path.join(image_path, "*"))

        def read_image(file_name):
            image_name = os.path.basename(file_name).split(".")[0]
            with open(file_name, "rb") as f:
                return image_name, f.read()

        images = {}

        pbar = tqdm(total=len(file_names), desc="Loading images", unit="image")

        with ThreadPoolExecutor(max_workers=num_thread) as executor:
            for image_name, image in executor.map(read_image, file_names):
                images[image_name] = image
                pbar.update(1)
        pbar.close()

        return images
