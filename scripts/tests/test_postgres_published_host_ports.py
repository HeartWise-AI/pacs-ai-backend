"""Off-host clients must not reach the Postgres services Compose publishes.

The deployment guide requires control-plane port 5433 and cardio-agent port
5434 to be loopback-only. These tests fail while the shipped Compose files
publish those ports, and the dev Orthanc index port, on every host interface.
"""

import os
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

import psycopg2

REPO_ROOT = Path(__file__).resolve().parents[2]
MIGRATIONS = REPO_ROOT / "api-pacs" / "infrastructures" / "database" / "postgresql" / "migrations"
PLACEHOLDER_PASSWORD = "placeholder-not-a-secret"
NETNS = "pg-exposure-attacker"
VETH_HOST = "pgexp-host"
VETH_ATTACKER = "pgexp-atk"
ATTACKER_V4 = "10.199.0.2"
HOST_VETH_V4 = "10.199.0.1"
ATTACKER_V6 = "fd00:199::2"
HOST_VETH_V6 = "fd00:199::1"
COMPOSE_ENV_KEYS = (
    "POSTGRES_PASSWORD",
    "APP_TIMEZONE",
    "ORTHANC_DICOM_PORT",
    "ORTHANC_DICOM_AET",
    "API_URL_REST_PORT",
)
INCLUDE_STUBS = (
    REPO_ROOT / "cardio-agent" / "backend" / "docker-compose.root.yml",
    REPO_ROOT / "cardio-agent" / "backend" / "docker-compose.root-dev.yml",
    REPO_ROOT / "cardio-agent" / "study-service" / "docker-compose.root.yaml",
    REPO_ROOT / "cardio-agent" / "study-service" / "docker-compose.root-dev.yaml",
    REPO_ROOT.parent / "PACS-AI" / "docker-compose.yml",
)


def _run(command, *, check=True, env=None, timeout=180):
    return subprocess.run(
        command,
        check=check,
        cwd=REPO_ROOT,
        env=env,
        text=True,
        capture_output=True,
        timeout=timeout,
    )


def _sudo(command, *, check=True, env=None, timeout=180):
    return _run(["sudo", *command], check=check, env=env, timeout=timeout)


def _compose_env():
    env = os.environ.copy()
    env["POSTGRES_PASSWORD"] = PLACEHOLDER_PASSWORD
    env["APP_TIMEZONE"] = "UTC"
    env["ORTHANC_DICOM_PORT"] = "4242"
    env["ORTHANC_DICOM_AET"] = "TESTAET"
    env["API_URL_REST_PORT"] = "8000"
    env.pop("POSTGRES_USER", None)
    return env


def _primary_host_ipv4():
    listed = _run(["ip", "-4", "-o", "addr", "show", "scope", "global"]).stdout
    for line in listed.splitlines():
        parts = line.split()
        iface = parts[1]
        if iface == "lo" or iface.startswith(("docker", "br-", "veth", "pgexp-")):
            continue
        return parts[3].split("/", 1)[0]
    raise RuntimeError("no non-loopback IPv4 address for an off-host client to target")


class PublishedPostgresExposureTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if shutil.which("docker") is None:
            raise unittest.SkipTest("docker is required to publish the Compose ports")
        cls.created_stubs = _install_include_stubs()
        cls.env_file = REPO_ROOT / "postgresql" / ".env"
        cls.env_file_existed = cls.env_file.exists()
        if not cls.env_file_existed:
            cls.env_file.write_text(
                "POSTGRES_PASSWORD=placeholder-not-a-secret\nAPP_TIMEZONE=UTC\n",
                encoding="utf-8",
            )
        cls.override_dir = tempfile.TemporaryDirectory(prefix="pg-exposure-")
        cls.override = Path(cls.override_dir.name) / "password-override.yml"
        cls.override.write_text(
            "services:\n"
            "  postgres:\n"
            "    environment:\n"
            "      POSTGRES_PASSWORD: placeholder-not-a-secret\n",
            encoding="utf-8",
        )
        cls.legacy_forward = _legacy_forward_policy()
        # Docker 29 programs iptables-nft. A leftover iptables-legacy FORWARD
        # DROP drops DNATed packets before those rules can accept them.
        if cls.legacy_forward == "DROP":
            _sudo(["iptables-legacy", "-P", "FORWARD", "ACCEPT"])
        _sudo(["docker", "network", "create", "pacs-net"], check=False)
        cls.attacker = _AttackerNetwork(_primary_host_ipv4())
        cls.attacker.start()

    @classmethod
    def tearDownClass(cls):
        attacker = getattr(cls, "attacker", None)
        if attacker is not None:
            attacker.stop()
        if getattr(cls, "legacy_forward", None) == "DROP":
            _sudo(["iptables-legacy", "-P", "FORWARD", "DROP"], check=False)
        override_dir = getattr(cls, "override_dir", None)
        if override_dir is not None:
            override_dir.cleanup()
        if getattr(cls, "env_file", None) is not None and not cls.env_file_existed:
            cls.env_file.unlink(missing_ok=True)
        _remove_include_stubs(getattr(cls, "created_stubs", ()))

    def test_production_compose_postgres_rejects_off_host_clients(self):
        self._assert_compose_ports_reject_off_host_clients(
            "docker-compose.yml",
            "pg-exposure-prod",
            (
                {
                    "service": "postgresql",
                    "container": "postgresql",
                    "port": 5433,
                    "user": "pacs",
                    "database": "db_pacs",
                    "migrations": True,
                },
                {
                    "service": "postgres",
                    "container": "cardio-postgres",
                    "port": 5434,
                    "user": "cardio",
                    "database": "cardio_agent",
                    "migrations": False,
                },
            ),
        )

    def test_dev_compose_postgres_rejects_off_host_clients(self):
        self._assert_compose_ports_reject_off_host_clients(
            "docker-compose-dev.yml",
            "pg-exposure-dev",
            (
                {
                    "service": "postgresql",
                    "container": "postgresql",
                    "port": 5433,
                    "user": "pacs",
                    "database": "db_pacs",
                    "migrations": True,
                },
                {
                    "service": "postgres",
                    "container": "cardio-postgres",
                    "port": 5434,
                    "user": "cardio",
                    "database": "cardio_agent",
                    "migrations": False,
                },
                {
                    "service": "orthanc-index",
                    "container": "orthanc-index",
                    "port": 5432,
                    "user": "postgres",
                    "database": "postgres",
                    "migrations": False,
                    "trust": True,
                },
            ),
        )

    def _assert_compose_ports_reject_off_host_clients(self, compose_file, project, targets):
        self._compose_down(compose_file, project)
        for target in targets:
            _sudo(["docker", "rm", "-f", target["container"]], check=False)
        self._compose(
            compose_file,
            project,
            ["up", "-d", *[target["service"] for target in targets]],
        )
        try:
            for target in targets:
                self._wait_ready(target)
                self._assert_unreachable(compose_file, target, "ipv4", self.attacker.host_v4)
                self._assert_unreachable(compose_file, target, "ipv6", HOST_VETH_V6)
        finally:
            self._compose_down(compose_file, project)

    def _assert_unreachable(self, compose_file, target, family, host):
        with self.subTest(compose=compose_file, service=target["service"], family=family):
            drop = family == "ipv4"
            if drop:
                _install_input_drop(target["port"])
            try:
                code, output = self.attacker.probe(host, target)
            finally:
                if drop:
                    _remove_input_drop(target["port"])
            published = _sudo(
                ["docker", "port", target["container"]],
                check=False,
            ).stdout.strip()
            self.assertEqual(
                code,
                0,
                "off-host client reached "
                f"{target['container']} via {family} {host}:{target['port']}\n"
                f"{output}\npublished: {published or '(none)'}",
            )

    def _wait_ready(self, target):
        deadline = time.monotonic() + 90
        last = ""
        while time.monotonic() < deadline:
            result = _sudo(
                [
                    "docker",
                    "exec",
                    target["container"],
                    "pg_isready",
                    "-U",
                    target["user"],
                    "-d",
                    target["database"],
                ],
                check=False,
            )
            last = (result.stdout or result.stderr).strip()
            if result.returncode == 0:
                return
            time.sleep(1)
        self.fail(f"{target['container']} did not become ready: {last}")

    def _compose(self, compose_file, project, args):
        env = _compose_env()
        command = [
            "sudo",
            "--preserve-env=" + ",".join(COMPOSE_ENV_KEYS),
            "docker",
            "compose",
            "-p",
            project,
            "-f",
            compose_file,
            "-f",
            str(self.override),
            *args,
        ]
        result = _run(command, env=env, check=False, timeout=300)
        if result.returncode != 0:
            self.fail(f"docker compose {' '.join(args)} failed\n{result.stderr}")

    def _compose_down(self, compose_file, project):
        self._compose(compose_file, project, ["down", "-v", "--remove-orphans"])


class _AttackerNetwork:
    def __init__(self, host_v4):
        self.host_v4 = host_v4

    def start(self):
        self.stop()
        _sudo(["ip", "netns", "add", NETNS])
        _sudo(["ip", "link", "add", VETH_HOST, "type", "veth", "peer", "name", VETH_ATTACKER])
        _sudo(["ip", "link", "set", VETH_ATTACKER, "netns", NETNS])
        _sudo(["ip", "addr", "add", f"{HOST_VETH_V4}/24", "dev", VETH_HOST])
        _sudo(["ip", "link", "set", VETH_HOST, "up"])
        _sudo(["ip", "-6", "addr", "add", f"{HOST_VETH_V6}/64", "dev", VETH_HOST])
        _sudo(
            [
                "ip",
                "netns",
                "exec",
                NETNS,
                "ip",
                "addr",
                "add",
                f"{ATTACKER_V4}/24",
                "dev",
                VETH_ATTACKER,
            ]
        )
        _sudo(["ip", "netns", "exec", NETNS, "ip", "link", "set", VETH_ATTACKER, "up"])
        _sudo(["ip", "netns", "exec", NETNS, "ip", "link", "set", "lo", "up"])
        _sudo(
            [
                "ip",
                "netns",
                "exec",
                NETNS,
                "ip",
                "-6",
                "addr",
                "add",
                f"{ATTACKER_V6}/64",
                "dev",
                VETH_ATTACKER,
            ]
        )
        _sudo(
            ["ip", "netns", "exec", NETNS, "ip", "route", "add", "default", "via", HOST_VETH_V4],
            check=False,
        )

    def stop(self):
        _sudo(["ip", "netns", "del", NETNS], check=False)
        _sudo(["ip", "link", "del", VETH_HOST], check=False)

    def probe(self, host, target):
        env = os.environ.copy()
        env.update(
            {
                "POSTGRES_EXPOSURE_PROBE": "1",
                "PROBE_HOST": host,
                "PROBE_PORT": str(target["port"]),
                "PROBE_USER": target["user"],
                "PROBE_DB": target["database"],
                "PROBE_TRUST": "1" if target.get("trust") else "0",
                "PROBE_MIGRATIONS": str(MIGRATIONS) if target["migrations"] else "",
                "PGPASSWORD": "" if target.get("trust") else PLACEHOLDER_PASSWORD,
            }
        )
        preserved = (
            "POSTGRES_EXPOSURE_PROBE,PROBE_HOST,PROBE_PORT,PROBE_USER,"
            "PROBE_DB,PROBE_TRUST,PROBE_MIGRATIONS,PGPASSWORD"
        )
        result = _run(
            [
                "sudo",
                f"--preserve-env={preserved}",
                "ip",
                "netns",
                "exec",
                NETNS,
                "python3",
                str(Path(__file__).resolve()),
            ],
            env=env,
            check=False,
            timeout=120,
        )
        output = "\n".join(part for part in (result.stdout, result.stderr) if part)
        return result.returncode, output


def _install_input_drop(port):
    for binary in ("iptables", "iptables-legacy"):
        if shutil.which(binary) is None:
            continue
        _sudo([binary, "-I", "INPUT", "-p", "tcp", "--dport", str(port), "-j", "DROP"])


def _remove_input_drop(port):
    for binary in ("iptables", "iptables-legacy"):
        if shutil.which(binary) is None:
            continue
        _sudo(
            [binary, "-D", "INPUT", "-p", "tcp", "--dport", str(port), "-j", "DROP"],
            check=False,
        )


def _legacy_forward_policy():
    if shutil.which("iptables-legacy") is None:
        return None
    result = _sudo(["iptables-legacy", "-S", "FORWARD"], check=False)
    if result.returncode != 0 or not result.stdout:
        return None
    first = result.stdout.splitlines()[0]
    if first.startswith("-P FORWARD "):
        return first.removeprefix("-P FORWARD ").strip()
    return None


def _install_include_stubs():
    created = []
    for path in INCLUDE_STUBS:
        if path.exists():
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("services: {}\n", encoding="utf-8")
        created.append(path)
    return created


def _remove_include_stubs(created):
    for path in created:
        path.unlink(missing_ok=True)
        parent = path.parent
        while parent not in {REPO_ROOT, REPO_ROOT.parent, Path("/")}:
            try:
                parent.rmdir()
            except OSError:
                break
            parent = parent.parent


def _probe():
    host = os.environ["PROBE_HOST"]
    port = int(os.environ["PROBE_PORT"])
    user = os.environ["PROBE_USER"]
    database = os.environ["PROBE_DB"]
    trust = os.environ.get("PROBE_TRUST") == "1"
    try:
        connection = psycopg2.connect(
            host=host,
            port=port,
            user=user,
            dbname=database,
            password=None if trust else os.environ.get("PGPASSWORD"),
            connect_timeout=5,
        )
    except psycopg2.OperationalError as exc:
        message = str(exc)
        if "authentication failed" in message or "no password supplied" in message:
            print(f"reachable {host}:{port} user={user}: server answered authentication")
            return 2
        # A refused handshake means this address is not accepting the published
        # port. A timeout is not that signal: a drop can hide a listener.
        refused = ("Connection refused", "Network is unreachable", "No route to host")
        if any(part in message for part in refused):
            print(f"unreachable {host}:{port}")
            return 0
        print(message)
        return 1

    connection.autocommit = True
    cursor = connection.cursor()
    try:
        migrations = os.environ.get("PROBE_MIGRATIONS") or ""
        if migrations:
            cursor.execute(
                "SELECT 1 FROM pg_type WHERE typname = 'inference_ingestion_job_status'"
            )
            if cursor.fetchone() is None:
                for path in sorted(Path(migrations).glob("*.up.sql")):
                    cursor.execute(path.read_text(encoding="utf-8"))
            cursor.execute(
                """
                INSERT INTO ingestion_jobs (
                    id, tenant_id, dicom_modality, container_id, model_id,
                    model_name, model_version, modalities,
                    schedule_start_timestamp, schedule_end_timestamp, status
                ) VALUES (
                    'exposure-probe', 'tenant-a', 'US', 'container', 'model',
                    'model-name', '1', ARRAY['US'], NOW(), NOW(), 'STOPPED'
                )
                """
            )
            cursor.execute(
                """
                INSERT INTO iam_account_links (
                    provider, external_uid, tenant_id, pacs_user_id, linked_by
                ) VALUES ('exposure-probe', 'ext-user', 'tenant-a', 'pacs-user', 'probe')
                """
            )
            cursor.execute(
                "SELECT tenant_id FROM ingestion_jobs WHERE id = %s",
                ("exposure-probe",),
            )
            print(f"wrote ingestion_jobs tenant={cursor.fetchone()[0]} via {host}:{port}")
            cursor.execute(
                "DELETE FROM iam_account_links WHERE provider = %s", ("exposure-probe",)
            )
            cursor.execute("DELETE FROM ingestion_jobs WHERE id = %s", ("exposure-probe",))
        else:
            cursor.execute("CREATE TABLE exposure_probe (id integer)")
            cursor.execute("INSERT INTO exposure_probe (id) VALUES (1)")
            cursor.execute("UPDATE exposure_probe SET id = 2")
            cursor.execute("SELECT id FROM exposure_probe")
            print(f"wrote exposure_probe id={cursor.fetchone()[0]} via {host}:{port}")
            cursor.execute("DROP TABLE exposure_probe")
        cursor.execute("SELECT inet_client_addr()")
        print(f"client={cursor.fetchone()[0]}")
    finally:
        connection.close()
    return 2


if __name__ == "__main__":
    if os.environ.get("POSTGRES_EXPOSURE_PROBE") == "1":
        raise SystemExit(_probe())
    unittest.main()
