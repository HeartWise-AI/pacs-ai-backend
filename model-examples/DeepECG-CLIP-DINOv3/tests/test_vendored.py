"""The ECG page renderer, the PDF reader and the image preprocessing are DeepECG_Docker's own files,
unchanged. Update them by copying the files of a newer DeepECG_Docker commit and these hashes."""
import hashlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEEPECG_DOCKER_COMMIT = "8f42a184e95bec63bd9bde9372428f926ab414f1"
VENDORED = {
    "utils/ecg_page_renderer.py": "df73a3a681d6e0e9abfd85bdf98ae87d3f2b31d43b7d4d6b99d481d8ea45bcfe",
    "utils/ecg_pdf.py": "ae8bbb4f67004ae40ef5071a912567924c99b7bbe9df7eddce76105e68c12996",
    "utils/image_preprocessing.py": "bd70c6a273bb3b33fc12ea0e0e268de6fb4f42b1d272f3e56499f28ed6ec9f4d",
}


def test_vendored_files_are_unchanged_copies_of_deepecg_docker():
    for path, sha256 in VENDORED.items():
        assert hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == sha256, path
