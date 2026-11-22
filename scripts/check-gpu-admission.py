#!/usr/bin/env python3
"""Read-only audit of deployed inference routing; never prints environment values."""
import json
import subprocess
import sys


def enabled(value):
    return str(value).lower() in {"true", "1", "t"}


def environment(container):
    return dict(item.split("=", 1) for item in container.get("Config", {}).get("Env", []) if "=" in item)


def check_inventory(containers):
    running = {item.get("Name", "").lstrip("/"): item for item in containers if item.get("State", {}).get("Running")}
    checks = []

    def check(name, passed, detail):
        checks.append({"check": name, "status": "PASS" if passed else "FAIL", "detail": detail})

    api = running.get("api-pacs")
    check("api-running", api is not None, "api-pacs must be running")
    env = environment(api or {})
    check("manager-enabled", enabled(env.get("MODEL_MANAGER_ENABLED")), "Manual inference must use GPU admission")
    # Omission selects the safe production default added with retained-peak accounting.
    check("retained-peak", enabled(env.get("MODEL_RETAIN_PEAK_WHILE_LOADED", "true")), "Loaded GPU models must retain peak reservations until confirmed unload")
    try:
        sweep = int(env.get("MODEL_IDLE_SWEEP_SECONDS", "0"))
    except ValueError:
        sweep = 0
    check("idle-sweeper", sweep > 0, "Periodic idle offloading must be enabled")
    callers = {name: item for name, item in running.items() if name == "study-service" or name == "study-celery-worker" or name.startswith("study-celery-worker-")}
    check("study-service-running", "study-service" in callers, "study-service must be running")
    check("workers-running", any(name.startswith("study-celery-worker") for name in callers), "At least one study worker must be running")
    for name, caller in sorted(callers.items()):
        check("gateway:" + name, enabled(environment(caller).get("PACS_AI_INFERENCE_GATEWAY_ENABLED")), "Every background GPU caller must use the shared admission gateway")
    return checks


def main():
    try:
        ids = subprocess.check_output(["docker", "ps", "-q"], text=True).split()
        containers = json.loads(subprocess.check_output(["docker", "inspect", *ids], text=True)) if ids else []
        checks = check_inventory(containers)
    except (subprocess.CalledProcessError, OSError, ValueError):
        print(json.dumps({"overall": "FAIL", "detail": "Cannot inspect running Docker services"}))
        return 1
    passed = all(item["status"] == "PASS" for item in checks)
    print(json.dumps({"overall": "PASS" if passed else "FAIL", "checks": checks}, indent=2))
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
