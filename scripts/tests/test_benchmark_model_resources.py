import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

import pydicom
from pydicom.dataset import FileDataset, FileMetaDataset
from pydicom.uid import CTImageStorage, ExplicitVRLittleEndian, generate_uid


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
BENCHMARK_PATH = REPOSITORY_ROOT / "scripts" / "benchmark-model-resources.py"


def load_benchmark_module():
    spec = importlib.util.spec_from_file_location("benchmark_model_resources", BENCHMARK_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {BENCHMARK_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


BENCHMARK = load_benchmark_module()


def write_dicom(
    path: Path,
    study_uid: str,
    series_uid: str,
    series_number: int,
    instance_number: int,
    z_position: float,
    padding_bytes: int = 0,
) -> None:
    file_meta = FileMetaDataset()
    file_meta.MediaStorageSOPClassUID = CTImageStorage
    file_meta.MediaStorageSOPInstanceUID = generate_uid()
    file_meta.TransferSyntaxUID = ExplicitVRLittleEndian

    dataset = FileDataset(str(path), {}, file_meta=file_meta, preamble=b"\0" * 128)
    dataset.SOPClassUID = CTImageStorage
    dataset.SOPInstanceUID = file_meta.MediaStorageSOPInstanceUID
    dataset.StudyInstanceUID = study_uid
    dataset.SeriesInstanceUID = series_uid
    dataset.SeriesNumber = series_number
    dataset.InstanceNumber = instance_number
    dataset.ImageOrientationPatient = [1, 0, 0, 0, 1, 0]
    dataset.ImagePositionPatient = [0, 0, z_position]
    if padding_bytes:
        dataset.add_new((0x0011, 0x1010), "OB", b"x" * padding_bytes)
    pydicom.dcmwrite(path, dataset, enforce_file_format=True)


class BenchmarkDicomOrderingTests(unittest.TestCase):
    def test_collects_one_study_and_orders_series_then_spatial_position(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            selected_study = generate_uid()
            other_study = generate_uid()
            first_series = generate_uid()
            second_series = generate_uid()

            expected_names = ["series-1.dcm", "series-2-low.dcm", "series-2-high.dcm"]
            write_dicom(root / expected_names[2], selected_study, second_series, 2, 20, 5.0)
            write_dicom(root / expected_names[0], selected_study, first_series, 1, 1, 0.0)
            write_dicom(root / expected_names[1], selected_study, second_series, 2, 10, 1.0)

            # Larger files from another study must not be mixed into the selected volume.
            write_dicom(root / "other-1.dcm", other_study, generate_uid(), 1, 1, 0.0, 4096)
            write_dicom(root / "other-2.dcm", other_study, generate_uid(), 2, 1, 0.0, 8192)

            selected = BENCHMARK.collect_dicom_files(root, maximum=3)

        self.assertEqual(expected_names, [path.name for path in selected])

    def test_requires_enough_files_from_one_study(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_dicom(root / "one.dcm", generate_uid(), generate_uid(), 1, 1, 0.0)
            write_dicom(root / "two.dcm", generate_uid(), generate_uid(), 1, 1, 0.0)

            with self.assertRaisesRegex(ValueError, "largest study has 1"):
                BENCHMARK.collect_dicom_files(root, maximum=2)


if __name__ == "__main__":
    unittest.main()
