import json
import os
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).parents[2]
SCRIPT_PATH = REPO_ROOT / "scripts" / "deploy-model.sh"
MODEL_PATH = REPO_ROOT / "model-examples" / "CathEF-CLIP"
DEEPCORO_SYNTAX_PATH = REPO_ROOT / "model-examples" / "DeepCORO-SYNTAX"


FAKE_CURL = r"""
#!/usr/bin/env python3
import json
import os
import sys
from pathlib import Path

args = sys.argv[1:]
body = sys.stdin.read() if "@-" in args else ""
log_path = Path(os.environ["FAKE_CURL_LOG"])
with log_path.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps({"args": args, "body": body}) + "\n")

url = args[-1]
if url.endswith("/v1/iam/login"):
    print(os.environ.get("FAKE_LOGIN_RESPONSE", json.dumps({
        "success": True,
        "data": {"sessionToken": "test-session-token"},
    })))
elif url.endswith("/v1/inference/model/list"):
    counter_path = Path(os.environ["FAKE_LIST_COUNTER"])
    count = int(counter_path.read_text(encoding="utf-8")) if counter_path.exists() else 0
    counter_path.write_text(str(count + 1), encoding="utf-8")
    if count == 0:
        print(json.dumps({"success": True, "data": []}))
    else:
        print(json.dumps({
            "success": True,
            "data": [{
                "id": "model-id",
                "name": "CathEF-CLIP",
                "container": {"id": "container-id"},
            }],
        }))
elif url.endswith("/v1/inference/model/add"):
    print(json.dumps({"success": True, "data": {}}))
elif url.endswith("/v1/inference/model/proxy/container/container-id/info"):
    print(json.dumps({"success": True, "data": {"version": "1.0.0"}}))
else:
    print(json.dumps({"success": False, "message": "Unexpected test URL: " + url}))
"""


class DeployModelAuthenticationTests(unittest.TestCase):
    def run_script(self, *, api_base_url="http://127.0.0.1:8000", login_response=None):
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        root = Path(temp_dir.name)
        fake_bin = root / "bin"
        fake_bin.mkdir()

        fake_curl = fake_bin / "curl"
        fake_curl.write_text(textwrap.dedent(FAKE_CURL).lstrip(), encoding="utf-8")
        fake_curl.chmod(0o755)

        fake_docker = fake_bin / "docker"
        fake_docker.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        fake_docker.chmod(0o755)

        env_file = root / ".env.deploy"
        env_file.write_text(
            textwrap.dedent(
                f"""
                DOCKERHUB_USER=heartwisehub
                IMAGE_PREFIX=pacs-ai
                API_BASE_URL={api_base_url}
                TENANT_ID=test-tenant
                PACS_ADMIN_EMAIL=admin@example.com
                PACS_ADMIN_PASSWORD=test-secret-password
                DEFAULT_OUTPUT_MODE=HTML
                """
            ).lstrip(),
            encoding="utf-8",
        )

        curl_log = root / "curl.jsonl"
        environment = os.environ.copy()
        environment.update(
            {
                "DEPLOY_ENV_FILE": str(env_file),
                "FAKE_CURL_LOG": str(curl_log),
                "FAKE_LIST_COUNTER": str(root / "list-counter"),
                "PATH": f"{fake_bin}:{environment['PATH']}",
            }
        )
        if login_response is not None:
            environment["FAKE_LOGIN_RESPONSE"] = json.dumps(login_response)

        result = subprocess.run(
            [str(SCRIPT_PATH), str(MODEL_PATH), "--register-only"],
            cwd=REPO_ROOT,
            env=environment,
            text=True,
            capture_output=True,
            check=False,
        )
        calls = []
        if curl_log.exists():
            calls = [
                json.loads(line) for line in curl_log.read_text(encoding="utf-8").splitlines()
            ]
        return result, calls

    def test_register_only_uses_current_server_login_contract(self):
        result, calls = self.run_script()

        self.assertEqual(0, result.returncode, result.stderr)
        login_calls = [call for call in calls if call["args"][-1].endswith("/v1/iam/login")]
        self.assertEqual(1, len(login_calls))
        self.assertEqual(
            {
                "tenantId": "test-tenant",
                "email": "admin@example.com",
                "password": "test-secret-password",
            },
            json.loads(login_calls[0]["body"]),
        )
        self.assertNotIn("identitytoolkit.googleapis.com", json.dumps(calls))
        self.assertNotIn("test-secret-password", result.stdout + result.stderr)
        self.assertIn("Authenticated.", result.stdout)
        self.assertIn("Model registered.", result.stdout)

    def test_login_failure_reports_error_code_and_challenge_without_secret(self):
        result, _ = self.run_script(
            login_response={
                "success": False,
                "message": "Additional verification is required.",
                "errorCode": "LOGIN_CHALLENGE_REQUIRED",
                "data": {"challengeRequired": True},
            }
        )

        self.assertNotEqual(0, result.returncode)
        self.assertIn("LOGIN_CHALLENGE_REQUIRED", result.stderr)
        self.assertIn("interactive login challenge required", result.stderr)
        self.assertNotIn("test-secret-password", result.stdout + result.stderr)

    def test_remote_plain_http_api_is_rejected_before_login(self):
        result, calls = self.run_script(api_base_url="http://pacs.example.com")

        self.assertNotEqual(0, result.returncode)
        self.assertIn("API_BASE_URL must use HTTPS", result.stderr)
        self.assertEqual([], calls)

    def test_loopback_prefix_cannot_hide_a_remote_plain_http_host(self):
        result, calls = self.run_script(api_base_url="http://localhost:8000@pacs.example.com")

        self.assertNotEqual(0, result.returncode)
        self.assertIn("API_BASE_URL must use HTTPS", result.stderr)
        self.assertEqual([], calls)

    def run_build_script(self, *, model_path=MODEL_PATH, publish_latest=False):
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        root = Path(temp_dir.name)
        fake_bin = root / "bin"
        fake_bin.mkdir()

        docker_log = root / "docker.json"
        fake_docker = fake_bin / "docker"
        fake_docker.write_text(
            textwrap.dedent(
                """
                #!/usr/bin/env python3
                import json
                import os
                import sys
                from pathlib import Path

                Path(os.environ["FAKE_DOCKER_LOG"]).write_text(
                    json.dumps(sys.argv[1:]), encoding="utf-8"
                )
                """
            ).lstrip(),
            encoding="utf-8",
        )
        fake_docker.chmod(0o755)

        env_file = root / ".env.deploy"
        env_file.write_text("DOCKERHUB_USER=heartwisehub\n", encoding="utf-8")
        token_file = root / "hf_token.txt"
        token_file.write_text("test-token\n", encoding="utf-8")

        environment = os.environ.copy()
        environment.update(
            {
                "DEPLOY_ENV_FILE": str(env_file),
                "FAKE_DOCKER_LOG": str(docker_log),
                "PATH": f"{fake_bin}:{environment['PATH']}",
            }
        )
        arguments = [
            str(SCRIPT_PATH),
            str(model_path),
            "--hf-token-file",
            str(token_file),
            "--build-only",
        ]
        if publish_latest:
            arguments.append("--publish-latest")
        result = subprocess.run(
            arguments,
            cwd=REPO_ROOT,
            env=environment,
            text=True,
            capture_output=True,
            check=False,
        )

        docker_args = None
        if docker_log.exists():
            docker_args = json.loads(docker_log.read_text(encoding="utf-8"))
        return result, docker_args, token_file

    def test_legacy_build_passes_token_as_secret_without_publishing_latest(self):
        result, docker_args, token_file = self.run_build_script()

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIsNotNone(docker_args)
        self.assertEqual("build", docker_args[0])
        self.assertIn(f"id=hf_token,src={token_file}", docker_args)
        self.assertIn("heartwisehub/pacs-ai-cathef-clip:1.0.0", docker_args)
        self.assertNotIn("heartwisehub/pacs-ai-cathef-clip:latest", docker_args)
        self.assertEqual(str(MODEL_PATH), docker_args[-1])

    def test_legacy_latest_alias_is_explicit(self):
        result, docker_args, _ = self.run_build_script(publish_latest=True)

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("heartwisehub/pacs-ai-cathef-clip:latest", docker_args)

    def test_provenance_aware_build_is_routed_to_release_command(self):
        result, docker_args, _ = self.run_build_script(model_path=DEEPCORO_SYNTAX_PATH)

        self.assertNotEqual(0, result.returncode)
        self.assertIn("scripts/release-model.py", result.stderr)
        self.assertIsNone(docker_args)


if __name__ == "__main__":
    unittest.main()
