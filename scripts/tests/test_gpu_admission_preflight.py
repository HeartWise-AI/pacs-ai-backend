import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("gpu_admission", Path(__file__).parents[1] / "check-gpu-admission.py")
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


def container(name, env):
    return {"Name": "/" + name, "State": {"Running": True}, "Config": {"Env": [key + "=" + value for key, value in env.items()]}}


class AdmissionPreflightTests(unittest.TestCase):
    def inventory(self):
        return [container("api-pacs", {"MODEL_MANAGER_ENABLED": "true", "MODEL_IDLE_SWEEP_SECONDS": "30"}), container("study-service", {"PACS_AI_INFERENCE_GATEWAY_ENABLED": "true"}), container("study-celery-worker-jepa", {"PACS_AI_INFERENCE_GATEWAY_ENABLED": "true"})]

    def test_managed_host_passes_without_printing_secrets(self):
        inventory = self.inventory()
        inventory[0]["Config"]["Env"].append("TOKEN=not-for-output")
        checks = audit.check_inventory(inventory)
        self.assertTrue(all(item["status"] == "PASS" for item in checks))
        self.assertNotIn("not-for-output", str(checks))

    def test_unmanaged_background_worker_fails_even_with_manager_enabled(self):
        inventory = self.inventory()
        inventory.append(container("study-celery-worker-legacy", {}))
        failed = [item["check"] for item in audit.check_inventory(inventory) if item["status"] == "FAIL"]
        self.assertEqual(failed, ["gateway:study-celery-worker-legacy"])

    def test_disabled_manager_sweeper_and_peak_policy_fail(self):
        inventory = self.inventory()
        inventory[0] = container("api-pacs", {"MODEL_RETAIN_PEAK_WHILE_LOADED": "false"})
        failed = [item["check"] for item in audit.check_inventory(inventory) if item["status"] == "FAIL"]
        self.assertEqual(failed, ["manager-enabled", "retained-peak", "idle-sweeper"])


if __name__ == "__main__":
    unittest.main()
