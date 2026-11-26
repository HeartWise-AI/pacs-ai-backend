"""Files vendored unchanged from DeepECG_Docker: the ECG page renderer, the PDF reader, the image
preprocessing, its logger, and the DINOv3 probe-head bank (models/dinov3_bank.py: head loading and
validation, head_probabilities). Update them by copying the files of a newer DeepECG_Docker commit and
these hashes."""
import hashlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEEPECG_DOCKER_COMMIT = "a46499c5aeab4752ff6421447346fa0853d27acc"
VENDORED = {
    "models/dinov3_bank.py": "e080921af3af3b39b24bc88bfe1074ddf6c7d70669d37e81e6d58cbc93c46997",
    "utils/ecg_page_renderer.py": "df73a3a681d6e0e9abfd85bdf98ae87d3f2b31d43b7d4d6b99d481d8ea45bcfe",
    "utils/ecg_pdf.py": "ae8bbb4f67004ae40ef5071a912567924c99b7bbe9df7eddce76105e68c12996",
    "utils/image_preprocessing.py": "934caacd748d5b7ee750f9635fcb2dac93e6c2ac514b24e8b9e8f1a1d8b23e45",
    "utils/log_config.py": "83dd67d313035777f7c140f6e57917ca268e4fc7b5c3cdec1b0a2ef11f0a4970",
}


def test_vendored_files_are_unchanged_copies_of_deepecg_docker():
    for path, sha256 in VENDORED.items():
        assert hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == sha256, path
