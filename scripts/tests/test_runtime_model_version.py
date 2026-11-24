"""Images stamped with PACS_AI_MODEL_VERSION must report that version at runtime.

release-model.py derives candidate versions (e.g. 1.0.1-rc.<n>) and verify_runtime_payload
rejects an image whose /inference/model-info still reports the manifest version.
"""

import re
import unittest
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
MODEL_ROOT = REPOSITORY_ROOT / "model-examples"


def stamped_model_directories():
    for dockerfile in sorted(MODEL_ROOT.rglob("Dockerfile")):
        if "PACS_AI_MODEL_VERSION" in dockerfile.read_text(encoding="utf-8"):
            yield dockerfile.parent


class RuntimeModelVersionTest(unittest.TestCase):
    def test_stamped_images_pass_the_version_to_model_info(self):
        directories = list(stamped_model_directories())
        self.assertTrue(directories)
        for directory in directories:
            main = directory / "main.py"
            if not main.is_file():
                continue
            source = main.read_text(encoding="utf-8")
            if "model_info_payload(" not in source:
                continue
            with self.subTest(model=str(directory.relative_to(MODEL_ROOT))):
                call = source[source.index("model_info_payload(") :]
                call = call[: call.index("\n)") + 2]
                self.assertRegex(
                    call,
                    re.compile(r'model_version=os\.getenv\("PACS_AI_MODEL_VERSION"\)'),
                )


if __name__ == "__main__":
    unittest.main()
