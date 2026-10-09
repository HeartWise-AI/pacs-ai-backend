import argparse
import contextlib
import importlib.util
import io
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = REPOSITORY_ROOT / "scripts" / "release-model.py"
WORKFLOW_PATH = REPOSITORY_ROOT / ".github" / "workflows" / "publish-model-image.yml"
DEEPCORO_SYNTAX_PATH = REPOSITORY_ROOT / "model-examples" / "DeepCORO-SYNTAX"


def load_release_module():
    spec = importlib.util.spec_from_file_location("pacs_ai_release_model", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


RELEASE = load_release_module()
SOURCE_REVISION = "a" * 40
MODEL_REVISION = "b" * 40
WEIGHTS_SHA256 = "c" * 64


class ReleaseModelTests(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.root = Path(self.temp_dir.name)
        self.model_dir = self.root / "model-examples" / "Example"
        (self.model_dir / "data").mkdir(parents=True)
        self.manifest = {
            "modelId": "Example",
            "modelName": "Example Model",
            "version": "1.2.3",
            "provenance": {
                "sourceRepository": "HeartWise-AI/pacs-ai-backend",
                "sourceRevision": None,
                "modelRepository": "heartwise/example-model",
                "modelRevision": MODEL_REVISION,
                "weightsPath": "release/models/model.pt",
                "weightsSha256": WEIGHTS_SHA256,
            },
        }
        self.write_manifest()

    def write_manifest(self):
        (self.model_dir / "data" / "model_info.json").write_text(
            json.dumps(self.manifest), encoding="utf-8"
        )

    def write_dockerfile(self, *, credential_arg=False, dockerignore=True):
        arguments = "\n".join(
            f"ARG {argument}" for argument in sorted(RELEASE.PROVENANCE_BUILD_ARGS)
        )
        credential = "\nARG HF_TOKEN" if credential_arg else ""
        (self.model_dir / "Dockerfile").write_text(
            f"FROM python:3.12-slim\n{arguments}{credential}\n"
            'ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"\n'
            "COPY download_model.py .\n"
            "RUN --mount=type=secret,id=hf_token python download_model.py "
            '--repository "${PACS_AI_MODEL_REPOSITORY}" '
            '--revision "${PACS_AI_MODEL_REVISION}" '
            '--weights-path "${PACS_AI_WEIGHTS_PATH}" '
            '--weights-sha256 "${PACS_AI_WEIGHTS_SHA256}"\n',
            encoding="utf-8",
        )
        if dockerignore:
            (self.model_dir / ".dockerignore").write_text(
                "hf_token.txt\n**/hf_token.txt\n", encoding="utf-8"
            )

    def metadata(self):
        return RELEASE.load_release_metadata(self.model_dir, SOURCE_REVISION)

    def test_valid_manifest_becomes_build_args_labels_and_runtime_provenance(self):
        metadata = self.metadata()

        self.assertEqual("1.2.3", metadata.version)
        self.assertEqual(SOURCE_REVISION, metadata.source_revision)
        self.assertEqual(SOURCE_REVISION, metadata.provenance["sourceRevision"])
        self.assertEqual(SOURCE_REVISION, metadata.build_args["PACS_AI_SOURCE_REVISION"])
        self.assertEqual(
            MODEL_REVISION,
            metadata.labels["ai.heartwise.model.revision"],
        )

    def test_deepcoro_syntax_adopts_the_generic_release_contract(self):
        metadata = RELEASE.load_release_metadata(DEEPCORO_SYNTAX_PATH, SOURCE_REVISION)
        dockerfile, uses_secret = RELEASE.validate_dockerfile_contract(DEEPCORO_SYNTAX_PATH)

        self.assertEqual("DeepCORO-SYNTAX", metadata.model_name)
        self.assertEqual("6.0.0", metadata.version)
        self.assertEqual(DEEPCORO_SYNTAX_PATH / "Dockerfile", dockerfile)
        self.assertTrue(uses_secret)

    def test_every_provenance_aware_model_adopts_the_release_contract(self):
        provenance_models = []
        for manifest_path in sorted(
            (REPOSITORY_ROOT / "model-examples").glob("**/data/model_info.json")
        ):
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            if "provenance" not in manifest:
                continue
            model_dir = manifest_path.parent.parent
            provenance_models.append(model_dir)
            with self.subTest(model=str(model_dir.relative_to(REPOSITORY_ROOT))):
                self.assertIsNone(manifest["provenance"]["sourceRevision"])
                RELEASE.load_release_metadata(model_dir, SOURCE_REVISION)
                RELEASE.validate_dockerfile_contract(model_dir)

        self.assertGreaterEqual(len(provenance_models), 1)

    def test_dirty_worktree_is_rejected_before_source_stamping(self):
        with (
            mock.patch.object(
                RELEASE, "git_output", return_value=" M model-examples/Example/logic.py"
            ),
            self.assertRaisesRegex(RELEASE.ReleaseError, "dirty Git worktree"),
        ):
            RELEASE.require_clean_worktree()

    def test_publish_source_must_be_reachable_from_main(self):
        failed = subprocess.CompletedProcess(["git", "merge-base"], 1, "", "not an ancestor")
        with (
            mock.patch.object(RELEASE, "run_command", return_value=failed),
            self.assertRaisesRegex(RELEASE.ReleaseError, "origin/master"),
        ):
            RELEASE.require_merged_source(SOURCE_REVISION)

    def test_source_repository_is_resolved_from_supported_github_remotes(self):
        for remote in (
            "https://github.com/HeartWise-AI/pacs-ai-backend.git",
            "ssh://git@github.com/HeartWise-AI/pacs-ai-backend.git",
            "git@github.com:HeartWise-AI/pacs-ai-backend.git",
        ):
            with (
                self.subTest(remote=remote),
                mock.patch.object(RELEASE, "git_output", return_value=remote),
            ):
                self.assertEqual(
                    "HeartWise-AI/pacs-ai-backend",
                    RELEASE.resolve_source_repository(),
                )

    def test_legacy_partial_and_unknown_provenance_are_rejected(self):
        cases = {
            "legacy": None,
            "partial": {"sourceRepository": "HeartWise-AI/pacs-ai-backend"},
            "unknown": {**self.manifest["provenance"], "branch": "main"},
        }
        for name, provenance in cases.items():
            with self.subTest(name=name):
                self.manifest["provenance"] = provenance
                self.write_manifest()
                with self.assertRaises(RELEASE.ReleaseError):
                    self.metadata()

    def test_mutable_revisions_invalid_hashes_and_unsafe_paths_are_rejected(self):
        cases = {
            "mutable model revision": {"modelRevision": "main"},
            "uppercase hash": {"weightsSha256": "C" * 64},
            "parent path": {"weightsPath": "release/../model.pt"},
            "stale source": {"sourceRevision": "d" * 40},
        }
        original = dict(self.manifest["provenance"])
        for name, overrides in cases.items():
            with self.subTest(name=name):
                self.manifest["provenance"] = {**original, **overrides}
                self.write_manifest()
                with self.assertRaises(RELEASE.ReleaseError):
                    self.metadata()

    def test_semantic_version_is_required(self):
        self.manifest["version"] = "latest"
        self.write_manifest()

        with self.assertRaisesRegex(RELEASE.ReleaseError, "semantic version"):
            self.metadata()

    def test_image_repository_is_restricted_and_cannot_include_a_tag(self):
        RELEASE.validate_image_repository("heartwisehub/pacs-ai-example", "heartwisehub")
        for repository in (
            "other/pacs-ai-example",
            "heartwisehub/PACS-AI-example",
            "heartwisehub/pacs-ai-example:1.0.0",
            "heartwisehub/pacs-ai-example@sha256:abc",
        ):
            with self.subTest(repository=repository), self.assertRaises(RELEASE.ReleaseError):
                RELEASE.validate_image_repository(repository, "heartwisehub")

    def test_dockerfile_requires_build_args_secret_mount_and_token_ignore(self):
        self.write_dockerfile()
        dockerfile, uses_secret = RELEASE.validate_dockerfile_contract(self.model_dir)
        self.assertEqual(self.model_dir / "Dockerfile", dockerfile)
        self.assertTrue(uses_secret)

        (self.model_dir / ".dockerignore").unlink()
        with self.assertRaisesRegex(RELEASE.ReleaseError, "dockerignore"):
            RELEASE.validate_dockerfile_contract(self.model_dir)

    def test_dockerfile_rejects_credentials_in_arg_or_env(self):
        self.write_dockerfile(credential_arg=True)

        with self.assertRaisesRegex(RELEASE.ReleaseError, "BuildKit secret"):
            RELEASE.validate_dockerfile_contract(self.model_dir)

    def test_dockerfile_must_feed_release_metadata_to_the_downloader(self):
        self.write_dockerfile()
        dockerfile = self.model_dir / "Dockerfile"
        dockerfile.write_text(
            dockerfile.read_text(encoding="utf-8").replace(
                '--revision "${PACS_AI_MODEL_REVISION}"', "--revision main"
            ),
            encoding="utf-8",
        )

        with self.assertRaisesRegex(RELEASE.ReleaseError, "--revision"):
            RELEASE.validate_dockerfile_contract(self.model_dir)

    def test_build_passes_identifiers_and_secret_file_without_reading_token(self):
        self.write_dockerfile()
        token_file = self.root / "hf_token.txt"
        token_file.write_text("super-secret-token", encoding="utf-8")
        commands = []

        def capture(command, **_kwargs):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, "", "")

        with mock.patch.object(RELEASE, "run_command", side_effect=capture):
            RELEASE.build_image(
                self.model_dir,
                self.model_dir / "Dockerfile",
                "heartwisehub/pacs-ai-example:1.2.3",
                self.metadata(),
                token_file,
            )

        flattened = json.dumps(commands)
        self.assertIn(f"id=hf_token,src={token_file}", flattened)
        self.assertIn(f"PACS_AI_MODEL_REVISION={MODEL_REVISION}", flattened)
        self.assertNotIn("super-secret-token", flattened)
        self.assertNotIn(":latest", flattened)

    def test_label_and_runtime_mismatches_are_rejected(self):
        metadata = self.metadata()
        RELEASE.verify_image_labels(metadata.labels, metadata)
        RELEASE.verify_runtime_payload(
            {
                "modelId": metadata.model_id,
                "version": metadata.version,
                "provenance": metadata.provenance,
            },
            metadata,
        )

        with self.assertRaisesRegex(RELEASE.ReleaseError, "label verification"):
            RELEASE.verify_image_labels({}, metadata)
        with self.assertRaisesRegex(RELEASE.ReleaseError, "Runtime provenance"):
            RELEASE.verify_runtime_payload(
                {
                    "modelId": metadata.model_id,
                    "version": metadata.version,
                    "provenance": {**metadata.provenance, "weightsSha256": "d" * 64},
                },
                metadata,
            )

    def test_packaged_weight_verification_uses_declared_identity_without_a_token(self):
        commands = []

        def capture(command, **_kwargs):
            commands.append(list(command))
            return subprocess.CompletedProcess(command, 0, "", "")

        with mock.patch.object(RELEASE, "run_command", side_effect=capture):
            RELEASE.verify_packaged_weights("heartwisehub/pacs-ai-example:1.2.3", self.metadata())

        command = commands[0]
        self.assertIn("--verify-only", command)
        self.assertIn(MODEL_REVISION, command)
        self.assertIn(WEIGHTS_SHA256, command)
        self.assertNotIn("--token", command)
        self.assertNotIn("--token-file", command)

    def test_runtime_verification_retries_an_early_connection_reset(self):
        metadata = self.metadata()
        payload = {
            "modelId": metadata.model_id,
            "version": metadata.version,
            "provenance": metadata.provenance,
        }

        def docker_command(command, **_kwargs):
            stdout = "127.0.0.1:49152\n" if command[1] == "port" else ""
            return subprocess.CompletedProcess(command, 0, stdout, "")

        with (
            mock.patch.object(RELEASE, "run_command", side_effect=docker_command),
            mock.patch.object(
                RELEASE,
                "fetch_runtime_model_info",
                side_effect=[ConnectionResetError("starting"), payload],
            ) as fetch,
            mock.patch.object(RELEASE.time, "sleep"),
        ):
            RELEASE.verify_runtime_image(
                "heartwisehub/pacs-ai-example:1.2.3",
                metadata,
                timeout_seconds=10,
            )

        self.assertEqual(2, fetch.call_count)

    def test_registry_digest_is_resolved_from_push_or_inspection(self):
        digest = "sha256:" + "d" * 64
        self.assertEqual(
            digest,
            RELEASE.resolve_pushed_digest(
                "heartwisehub/pacs-ai-example",
                f"latest: digest: {digest} size: 1234",
                [],
            ),
        )
        self.assertEqual(
            digest,
            RELEASE.resolve_pushed_digest(
                "heartwisehub/pacs-ai-example",
                "push complete",
                [f"heartwisehub/pacs-ai-example@{digest}"],
            ),
        )

    def test_release_evidence_contains_immutable_identity_and_no_credentials(self):
        digest = "sha256:" + "d" * 64
        evidence = RELEASE.build_evidence(
            metadata=self.metadata(),
            image="heartwisehub/pacs-ai-example:1.2.3",
            image_id="sha256:" + "e" * 64,
            digest=digest,
            aliases=[],
        )

        self.assertEqual("published", evidence["status"])
        self.assertEqual(digest, evidence["image"]["registryDigest"])
        self.assertEqual(
            f"heartwisehub/pacs-ai-example@{digest}",
            evidence["image"]["immutableReference"],
        )
        serialized = json.dumps(evidence).lower()
        self.assertNotIn("token", serialized)
        self.assertNotIn("password", serialized)

    def test_publish_refuses_an_existing_version_before_build(self):
        self.write_dockerfile()
        token_file = self.root / "hf_token.txt"
        token_file.write_text("secret", encoding="utf-8")
        args = argparse.Namespace(
            model_dir=str(self.model_dir),
            image_repository="heartwisehub/pacs-ai-example",
            allowed_namespace="heartwisehub",
            hf_token_file=str(token_file),
            publish=True,
            publish_latest=False,
            evidence=str(self.root / "evidence.json"),
            runtime_timeout_seconds=1,
        )

        with (
            mock.patch.object(RELEASE, "REPOSITORY_ROOT", self.root),
            mock.patch.object(RELEASE, "require_clean_worktree"),
            mock.patch.object(RELEASE, "resolve_source_revision", return_value=SOURCE_REVISION),
            mock.patch.object(RELEASE, "require_merged_source"),
            mock.patch.object(RELEASE, "remote_version_exists", return_value=True),
            mock.patch.object(RELEASE, "build_image") as build,
            self.assertRaisesRegex(RELEASE.ReleaseError, "Refusing to overwrite"),
        ):
            RELEASE.release(args)
        build.assert_not_called()

    def test_release_verifies_runtime_before_publishing_and_writes_digest_evidence(self):
        self.write_dockerfile()
        token_file = self.root / "hf_token.txt"
        token_file.write_text("secret", encoding="utf-8")
        evidence_path = self.root / "evidence.json"
        args = argparse.Namespace(
            model_dir=str(self.model_dir),
            image_repository="heartwisehub/pacs-ai-example",
            allowed_namespace="heartwisehub",
            hf_token_file=str(token_file),
            publish=True,
            publish_latest=False,
            evidence=str(evidence_path),
            runtime_timeout_seconds=1,
        )
        digest = "sha256:" + "d" * 64
        image_id = "sha256:" + "e" * 64
        events = []

        with (
            mock.patch.object(RELEASE, "REPOSITORY_ROOT", self.root),
            mock.patch.object(RELEASE, "require_clean_worktree"),
            mock.patch.object(RELEASE, "resolve_source_revision", return_value=SOURCE_REVISION),
            mock.patch.object(
                RELEASE, "resolve_source_repository", return_value="HeartWise-AI/pacs-ai-backend"
            ),
            mock.patch.object(RELEASE, "require_merged_source"),
            mock.patch.object(RELEASE, "remote_version_exists", return_value=False),
            mock.patch.object(
                RELEASE,
                "build_image",
                side_effect=lambda *_args: events.append("build"),
            ),
            mock.patch.object(
                RELEASE,
                "inspect_image",
                return_value=(image_id, self.metadata().labels, []),
            ),
            mock.patch.object(
                RELEASE,
                "verify_packaged_weights",
                side_effect=lambda *_args: events.append("weights"),
            ),
            mock.patch.object(
                RELEASE,
                "verify_runtime_image",
                side_effect=lambda *_args: events.append("runtime"),
            ),
            mock.patch.object(
                RELEASE,
                "publish_image",
                side_effect=lambda *_args: (events.append("publish") or (digest, [])),
            ),
        ):
            evidence = RELEASE.release(args)

        self.assertEqual(["build", "weights", "runtime", "publish"], events)
        self.assertEqual(digest, evidence["image"]["registryDigest"])
        self.assertEqual(evidence, json.loads(evidence_path.read_text(encoding="utf-8")))

    def test_publish_latest_requires_an_explicit_publish(self):
        with self.assertRaises(SystemExit), contextlib.redirect_stderr(io.StringIO()):
            RELEASE.parse_args(
                [
                    str(self.model_dir),
                    "--image-repository",
                    "heartwisehub/pacs-ai-example",
                    "--publish-latest",
                ]
            )

    def test_release_workflow_uses_protected_self_hosted_credentials(self):
        workflow = WORKFLOW_PATH.read_text(encoding="utf-8")

        self.assertIn("workflow_dispatch:", workflow)
        self.assertIn("runs-on: [self-hosted, linux, x64, pacs-ai-release]", workflow)
        self.assertIn("environment: model-release", workflow)
        self.assertIn("github.ref == 'refs/heads/master'", workflow)
        self.assertIn("/etc/pacs-ai-release/secrets/hf_token", workflow)
        self.assertIn("--password-stdin", workflow)
        self.assertIn("scripts/release-model.py", workflow)
        self.assertIn("--publish", workflow)
        self.assertNotIn("secrets.HF_TOKEN", workflow)
        self.assertNotIn("secrets.DOCKER", workflow)

        action_references = [
            line.strip().removeprefix("uses: ").split(" #", maxsplit=1)[0]
            for line in workflow.splitlines()
            if line.strip().startswith("uses:")
        ]
        self.assertGreaterEqual(len(action_references), 2)
        for reference in action_references:
            revision = reference.rsplit("@", maxsplit=1)[-1]
            self.assertRegex(revision, r"^[0-9a-f]{40}$")


if __name__ == "__main__":
    unittest.main()
