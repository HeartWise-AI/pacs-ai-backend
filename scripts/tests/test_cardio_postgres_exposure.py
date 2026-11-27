import json
import os
import re
import shutil
import socket
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
COMPOSE_FILES = ("docker-compose.yml", "docker-compose-dev.yml")
LOOPBACK_HOSTS = {"127.0.0.1", "::1"}
ENV_REFERENCE = re.compile(r"^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$")
ATTACK_PLACEHOLDER = "placeholder-not-a-real-secret"
WRITE_MARKER = "exposure-test-tamper"


def _docker_command(args):
    binary = shutil.which("docker") or "/usr/bin/docker"
    if os.access(binary, os.X_OK):
        probe = subprocess.run([binary, "info"], capture_output=True, text=True)
        if probe.returncode == 0:
            return [binary, *args]
    if shutil.which("sudo"):
        return ["sudo", binary, *args]
    raise AssertionError("docker is required")


def _run(args, env=None, check=True):
    command = _docker_command(args)
    try:
        proc = subprocess.run(command, capture_output=True, text=True, env=env)
    except FileNotFoundError:
        command = ["sudo", "/usr/bin/docker", *args]
        proc = subprocess.run(command, capture_output=True, text=True, env=env)
    if proc.returncode != 0 and "permission denied" in (proc.stderr or "").lower():
        command = ["sudo", "/usr/bin/docker", *args]
        proc = subprocess.run(command, capture_output=True, text=True, env=env)
    if check and proc.returncode != 0:
        detail = proc.stderr or ""
        raise AssertionError(f"command failed ({proc.returncode}): {' '.join(command)}\n{detail}")
    return proc


def _postgres_block(text):
    lines = text.splitlines()
    start = next((i + 1 for i, line in enumerate(lines) if line.startswith("  postgres:")), None)
    if start is None:
        raise AssertionError("postgres service is missing")
    block = []
    for line in lines[start:]:
        if line and not line.startswith(" "):
            break
        block.append(line)
    return "\n".join(block)


def _mapping_value(block, key):
    match = re.search(rf"^\s+{re.escape(key)}:\s*(\S+)\s*$", block, re.M)
    if match is None:
        raise AssertionError(f"{key} is missing from the postgres service")
    return match.group(1).strip("'\"")


def _port_specs(block):
    if re.search(r"^\s+ports:\s*\[\s*\]\s*$", block, re.M):
        return []
    if not re.search(r"^\s+ports:\s*$", block, re.M):
        return []
    specs = []
    in_ports = False
    for line in block.splitlines():
        if re.match(r"^\s+ports:\s*$", line):
            in_ports = True
            continue
        if not in_ports:
            continue
        item = re.match(r"""^\s+-\s+["']?(.+?)["']?\s*$""", line)
        if item:
            specs.append(item.group(1))
            continue
        if line.strip():
            break
    return specs


def _ports_are_loopback(block):
    specs = _port_specs(block)
    if specs:
        return all(
            spec.startswith("127.0.0.1:") or spec.startswith("[::1]:") for spec in specs
        )
    if not re.search(r"^\s+ports:\s*$", block, re.M):
        return True
    host_ips = [
        value.strip("'\"")
        for value in re.findall(r"^\s+host_ip:\s*(\S+)\s*$", block, re.M)
    ]
    return bool(host_ips) and all(ip in LOOPBACK_HOSTS for ip in host_ips)


def _include_paths(compose_path):
    text = compose_path.read_text()
    if "include:" not in text:
        return []
    section = text.split("include:", 1)[1].split("\nvolumes:", 1)[0]
    paths = []
    for line in section.splitlines():
        match = re.match(r"^\s+-\s+(\S+)\s*$", line)
        if match:
            paths.append((compose_path.parent / match.group(1)).resolve())
    return paths


def _non_loopback_ip():
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        sock.connect(("192.0.2.1", 9))
        return sock.getsockname()[0]
    finally:
        sock.close()


class CardioPostgresComposeContractTest(unittest.TestCase):
    def test_password_is_an_environment_reference(self):
        for name in COMPOSE_FILES:
            with self.subTest(compose=name):
                block = _postgres_block((REPO / name).read_text())
                value = _mapping_value(block, "POSTGRES_PASSWORD")
                self.assertTrue(
                    ENV_REFERENCE.fullmatch(value) is not None,
                    f"{name}: POSTGRES_PASSWORD must be an environment reference, "
                    f"got a literal of length {len(value)}",
                )

    def test_published_port_is_loopback_or_absent(self):
        for name in COMPOSE_FILES:
            with self.subTest(compose=name):
                block = _postgres_block((REPO / name).read_text())
                self.assertTrue(
                    _ports_are_loopback(block),
                    f"{name}: host port 5434 is published on every interface",
                )


class CardioPostgresRemoteAccessTest(unittest.TestCase):
    created = []

    @classmethod
    def setUpClass(cls):
        for name in COMPOSE_FILES:
            for path in _include_paths(REPO / name):
                if path.exists():
                    continue
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("services: {}\n")
                cls.created.append(path)
        for relative in ("postgresql/.env", "nginx/.env", "api-pacs/.env"):
            path = REPO / relative
            if path.exists():
                continue
            path.write_text("POSTGRES_PASSWORD=placeholder-not-the-cardio-password\n")
            cls.created.append(path)
        inspect = _run(["network", "inspect", "pacs-net"], check=False)
        if inspect.returncode != 0:
            _run(["network", "create", "pacs-net"])

    @classmethod
    def tearDownClass(cls):
        for path in reversed(cls.created):
            if path.is_file():
                path.unlink()
        _run(["rm", "-f", "cardio-postgres"], check=False)

    def test_remote_repository_credential_cannot_modify_pipeline_tables(self):
        for name in COMPOSE_FILES:
            with self.subTest(compose=name):
                try:
                    self._assert_remote_write_rejected(name)
                finally:
                    _run(
                        [
                            "compose",
                            "-p",
                            "cardio-exposure-test",
                            "-f",
                            name,
                            "down",
                            "-v",
                            "--remove-orphans",
                        ],
                        check=False,
                    )
                    _run(["rm", "-f", "cardio-postgres"], check=False)

    def _assert_remote_write_rejected(self, name):
        compose_path = REPO / name
        block = _postgres_block(compose_path.read_text())
        source_password = _mapping_value(block, "POSTGRES_PASSWORD")
        reference = ENV_REFERENCE.fullmatch(source_password)
        secrets = []
        with tempfile.TemporaryDirectory() as tmp:
            env_file = Path(tmp) / "compose.env"
            lines = [
                "APP_TIMEZONE=UTC",
                "ORTHANC_DICOM_PORT=4242",
                "ORTHANC_DICOM_AET=TESTAET",
                "API_URL_REST_PORT=8000",
                "POSTGRES_PASSWORD=placeholder-not-the-cardio-password",
            ]
            if reference:
                lines.append(f"{reference.group(1)}={ATTACK_PLACEHOLDER}-runtime")
                secrets.append(f"{ATTACK_PLACEHOLDER}-runtime")
            else:
                secrets.append(source_password)
            env_file.write_text("\n".join(lines) + "\n")
            _run(["rm", "-f", "cardio-postgres"], check=False)
            _run(
                [
                    "compose",
                    "--env-file",
                    str(env_file),
                    "-p",
                    "cardio-exposure-test",
                    "-f",
                    str(compose_path),
                    "up",
                    "-d",
                    "--wait",
                    "--wait-timeout",
                    "90",
                    "postgres",
                ]
            )
            rendered = _run(
                [
                    "compose",
                    "--env-file",
                    str(env_file),
                    "-p",
                    "cardio-exposure-test",
                    "-f",
                    str(compose_path),
                    "config",
                    "--format",
                    "json",
                ]
            )
            service = json.loads(rendered.stdout)["services"]["postgres"]
            user = service["environment"]["POSTGRES_USER"]
            database = service["environment"]["POSTGRES_DB"]
            container = service.get("container_name", "cardio-postgres")
            inspected = _run(
                [
                    "inspect",
                    "--format",
                    "{{json .NetworkSettings.Ports}}",
                    container,
                ]
            )
            ports = json.loads(inspected.stdout)
            published = [
                binding
                for bindings in ports.values()
                for binding in (bindings or [])
            ]
            attack_password = ATTACK_PLACEHOLDER if reference else source_password
            host_port = "5434"
            if published:
                host_port = published[0].get("HostPort") or host_port
            statement = (
                "CREATE TABLE IF NOT EXISTS pipeline_jobs ("
                "id integer primary key, study_instance_uid text, status text);"
                "CREATE TABLE IF NOT EXISTS pipeline_results ("
                "id integer primary key, job_id integer, payload text);"
                "INSERT INTO pipeline_jobs (id, study_instance_uid, status) "
                "VALUES (1, 'synthetic', 'stored') "
                "ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status;"
                f"UPDATE pipeline_jobs SET status = '{WRITE_MARKER}' WHERE id = 1;"
                "SELECT status FROM pipeline_jobs WHERE id = 1;"
                "INSERT INTO pipeline_results (id, job_id, payload) "
                "VALUES (1, 1, 'synthetic') "
                "ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload;"
                "DELETE FROM pipeline_results WHERE id = 1;"
                "DELETE FROM pipeline_jobs WHERE id = 1;"
            )
            client_env = os.environ.copy()
            client_env["PGPASSWORD"] = attack_password
            client_env["PGCONNECT_TIMEOUT"] = "3"
            client = subprocess.run(
                [
                    "psql",
                    "-h",
                    _non_loopback_ip(),
                    "-p",
                    str(host_port),
                    "-U",
                    user,
                    "-d",
                    database,
                    "-v",
                    "ON_ERROR_STOP=1",
                    "-X",
                    "-c",
                    statement,
                ],
                capture_output=True,
                text=True,
                env=client_env,
            )
            output = _scrub(client.stdout + client.stderr, secrets)
            if client.returncode == 127 or "not found" in output:
                self.fail("psql is required to attempt the remote connection")
            self.assertNotIn(
                WRITE_MARKER,
                client.stdout,
                f"{name}: a remote client using the repository credential modified "
                f"pipeline_jobs and pipeline_results\n{output}",
            )
            self.assertNotEqual(
                client.returncode,
                0,
                f"{name}: a remote client using the repository credential "
                "connected to the cardio database",
            )
            for binding in published:
                self.assertIn(
                    binding.get("HostIp"),
                    LOOPBACK_HOSTS,
                    f"{name} published {_scrub(json.dumps(binding), secrets)}",
                )


def _scrub(text, secrets):
    for secret in secrets:
        if secret:
            text = text.replace(secret, "<redacted>")
    return text


if __name__ == "__main__":
    unittest.main()
