"""Shared guard for tests that remove containers by their pinned container_name.

The compose files pin container_name (cardio-postgres, postgresql, orthanc-index),
so a test that runs `docker rm -f <name>` on a host that also runs PACS-AI would
delete the production database container. Skip instead.
"""
import shutil
import subprocess
import unittest

ALLOWED_PROJECT_PREFIXES = ("cardio-exposure-test", "pg-exposure-")


def refuse_foreign_containers(names, docker=None):
    docker = docker or [shutil.which("docker") or "docker"]
    for name in names:
        result = subprocess.run(
            [*docker, "inspect", "--format",
             '{{ index .Config.Labels "com.docker.compose.project" }}', name],
            capture_output=True, text=True, check=False,
        )
        if result.returncode != 0:
            continue  # no such container
        project = result.stdout.strip()
        if not project.startswith(ALLOWED_PROJECT_PREFIXES):
            raise unittest.SkipTest(
                f"container {name!r} belongs to compose project {project or '<none>'!r}; "
                "run these tests on a Docker host without a PACS-AI stack (e.g. docker:dind)"
            )
