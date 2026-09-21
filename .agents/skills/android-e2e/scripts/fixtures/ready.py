import json
import time
from pathlib import Path
import subprocess

for _ in range(60):
    path = Path("/run/android/status.json")
    status = json.loads(path.read_text()) if path.exists() else {}
    if status.get("state") == "failed":
        raise RuntimeError(status)
    if status.get("state") == "ready":
        endpoint = Path("/run/android/adb-address").read_text().strip()
        boot = subprocess.check_output(
            ["adb", "-s", endpoint, "shell", "getprop", "sys.boot_completed"],
            text=True,
            timeout=15,
        ).strip()
        if boot == "1":
            print("ANDROID_READY", status, flush=True)
            break
    print("Waiting for Android:", status, flush=True)
    time.sleep(5)
else:
    raise RuntimeError("Android readiness timed out")
