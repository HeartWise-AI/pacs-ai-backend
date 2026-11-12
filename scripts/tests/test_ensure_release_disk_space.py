import importlib.util
import subprocess
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "ensure_release_disk_space", Path(__file__).parents[1] / "ensure-release-disk-space.py"
)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ReleaseDiskSpaceTest(unittest.TestCase):
    def usage(self, gib):
        return SimpleNamespace(free=gib * module.GIB)

    @patch.object(module.subprocess, "run")
    @patch.object(module.shutil, "disk_usage")
    def test_sufficient_space_does_not_prune(self, usage, run):
        usage.return_value = self.usage(25)
        module.ensure_space(Path("/runner"), 25)
        run.assert_not_called()

    @patch.object(module.subprocess, "run")
    @patch.object(module.shutil, "disk_usage")
    def test_low_space_reclaims_only_bounded_old_build_cache(self, usage, run):
        usage.side_effect = [self.usage(18), self.usage(30)]
        module.ensure_space(Path("/runner"), 25)
        run.assert_called_once_with(
            ["docker", "builder", "prune", "--force", "--filter", "until=24h", "--keep-storage", "10GB"],
            check=True,
        )

    @patch.object(module.subprocess, "run")
    @patch.object(module.shutil, "disk_usage")
    def test_cleanup_cannot_bypass_required_space(self, usage, run):
        usage.side_effect = [self.usage(18), self.usage(20)]
        with self.assertRaisesRegex(RuntimeError, "Insufficient runner disk space"):
            module.ensure_space(Path("/runner"), 25)
        run.assert_called_once()

    @patch.object(module.subprocess, "run")
    @patch.object(module.shutil, "disk_usage")
    def test_prune_failure_stops_the_build(self, usage, run):
        usage.return_value = self.usage(18)
        run.side_effect = subprocess.CalledProcessError(1, "docker")
        with self.assertRaises(subprocess.CalledProcessError):
            module.ensure_space(Path("/runner"), 25)

    @patch.object(module.shutil, "disk_usage")
    def test_invalid_minimum_does_not_touch_disk(self, usage):
        with self.assertRaises(ValueError):
            module.ensure_space(Path("/runner"), 0)
        usage.assert_not_called()
