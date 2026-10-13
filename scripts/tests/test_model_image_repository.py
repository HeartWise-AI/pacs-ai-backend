import contextlib
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = REPOSITORY_ROOT / "scripts" / "model_image_repository.py"
MAPPING_PATH = REPOSITORY_ROOT / ".github" / "model-image-repositories.json"


def load_module():
    spec = importlib.util.spec_from_file_location("pacs_ai_model_repository", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


MAPPING = load_module()


class ModelImageRepositoryTests(unittest.TestCase):
    def write_mapping(self, root: Path, payload: object) -> Path:
        path = root / "mapping.json"
        path.write_text(json.dumps(payload), encoding="utf-8")
        return path

    def test_repository_mapping_resolves_documented_model(self):
        repository = MAPPING.resolve_image_repository(
            REPOSITORY_ROOT / "model-examples" / "DeepCORO-SYNTAX",
            mapping_path=MAPPING_PATH,
            repository_root=REPOSITORY_ROOT,
        )
        self.assertEqual("heartwisehub/pacs-ai-deepcoro-syntax", repository)

    def test_every_mapping_entry_identifies_an_existing_model_contract(self):
        repositories = MAPPING.load_repository_mapping(MAPPING_PATH)
        self.assertGreaterEqual(len(repositories), 1)
        for model_directory, image_repository in repositories.items():
            with self.subTest(model=model_directory, repository=image_repository):
                path = REPOSITORY_ROOT / model_directory
                self.assertTrue((path / "Dockerfile").is_file())
                self.assertTrue((path / "data" / "model_info.json").is_file())

    def test_unmapped_model_is_rejected(self):
        with self.assertRaisesRegex(
            MAPPING.ModelImageRepositoryError,
            "not approved",
        ):
            MAPPING.resolve_image_repository(
                REPOSITORY_ROOT / "model-examples" / "CIED-AI",
                mapping_path=MAPPING_PATH,
                repository_root=REPOSITORY_ROOT,
            )

    def test_model_directory_cannot_escape_repository(self):
        with self.assertRaisesRegex(
            MAPPING.ModelImageRepositoryError,
            "inside the repository",
        ):
            MAPPING.model_directory_key(Path("/tmp/outside"), REPOSITORY_ROOT)

    def test_mapping_rejects_invalid_or_duplicate_repositories(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            cases = (
                {
                    "schemaVersion": 2,
                    "repositories": {"model-examples/A": "heartwisehub/a"},
                },
                {
                    "schemaVersion": 1,
                    "repositories": {"../A": "heartwisehub/a"},
                },
                {
                    "schemaVersion": 1,
                    "repositories": {"model-examples/A": "HeartWiseHub/A"},
                },
                {
                    "schemaVersion": 1,
                    "repositories": {
                        "model-examples/A": "heartwisehub/shared",
                        "model-examples/B": "heartwisehub/shared",
                    },
                },
            )
            for index, payload in enumerate(cases):
                with self.subTest(case=index), self.assertRaises(
                    MAPPING.ModelImageRepositoryError
                ):
                    MAPPING.load_repository_mapping(self.write_mapping(root, payload))

    def test_cli_prints_only_the_approved_repository(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            result = MAPPING.main(
                [
                    "model-examples/DeepCORO-SYNTAX",
                    "--mapping",
                    str(MAPPING_PATH),
                ]
            )
        self.assertEqual(0, result)
        self.assertEqual("heartwisehub/pacs-ai-deepcoro-syntax\n", output.getvalue())


if __name__ == "__main__":
    unittest.main()
