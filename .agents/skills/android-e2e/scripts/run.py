#!/usr/bin/env python3
"""Exercise an immutable Android development image using a disposable sandbox."""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time
import uuid


class Runner:
    def __init__(self, image, workspace="chris", region="us-was-1", output_dir=None):
        if not re.fullmatch(r"blaxel/android:develop-[0-9a-f]{7,40}", image):
            raise ValueError("Use an immutable blaxel/android:develop-<commit SHA> image")
        self.image, self.workspace, self.region = image, workspace, region
        self.name = "android-e2e-" + uuid.uuid4().hex[:16]
        self.output = Path(output_dir or tempfile.mkdtemp(prefix="android-e2e-results-")).resolve()
        self.output.mkdir(parents=True, exist_ok=True)
        self.fixtures = Path(__file__).resolve().parent / "fixtures"
        self.cwd = None
        self.serial = 0
        self.results = {}

    def save(self, name, data):
        path = self.output / name
        path.write_text(json.dumps(data, indent=2) + "\n")
        return path

    def cli(self, *args, timeout=45):
        env = dict(os.environ, BL_ENV="dev")
        result = subprocess.run(["bl", *args, "-w", self.workspace, "-o", "json"],
                                cwd=self.cwd, env=env, capture_output=True, text=True,
                                timeout=timeout, check=False)
        if result.returncode:
            raise RuntimeError(f"bl {args[0]} failed ({result.returncode}): {result.stderr.strip()}")
        response = json.loads(result.stdout) if result.stdout.strip() else None
        if args[0] in ("apply", "delete") and isinstance(response, dict):
            if response.get("success") is False or response.get("failed"):
                raise RuntimeError(f"bl {args[0]} returned failure: {response}")
        return response

    def api(self, path, payload=None, timeout=40):
        args = ["run", "sandbox", self.name, "--env-file", "/dev/null", "--path", path,
                "--timeout", str(max(1, int(timeout) - 5))]
        if payload is None:
            args.extend(["--method", "GET"])
        else:
            self.serial += 1
            file = self.save(f"request-{self.serial:03}.json", payload)
            args.extend(["--file", str(file)])
        return self.cli(*args, timeout=timeout)

    def wait_health(self, seconds=240):
        deadline = time.monotonic() + seconds
        last = None
        while time.monotonic() < deadline:
            try:
                response = self.api("/health", timeout=min(20, max(1, deadline-time.monotonic())))
                self.save("health.json", response)
                if not isinstance(response, dict) or response.get("status") != "ok":
                    raise RuntimeError(f"Unhealthy API response: {response}")
                return
            except (RuntimeError, subprocess.TimeoutExpired) as error:
                last = str(error)
                print(f"Waiting for {self.name} API: {last}", flush=True)
            time.sleep(min(5, max(0, deadline-time.monotonic())))
        raise TimeoutError(f"Sandbox health deadline exceeded: {last}")

    def fixture(self, name, label=None, daemon=False):
        label = label or name
        payload = json.loads((self.fixtures / f"{name}.json").read_text())
        source = payload.pop("commandFile", None)
        if source:
            path = (self.fixtures / source).resolve()
            if not path.is_relative_to(self.fixtures.resolve()):
                raise ValueError("Fixture commandFile must be inside fixtures")
            interpreter = "python3" if path.suffix == ".py" else "bash"
            payload["command"] = interpreter + " -c " + shlex.quote(path.read_text())
        self.serial += 1
        payload["name"] = f"e2e-{label}-{self.serial}"
        payload["waitForCompletion"] = False
        seconds = int(payload.get("timeout", 120))
        if seconds <= 0 and not daemon:
            raise ValueError("Fixture timeout must be finite and positive")
        deadline = time.monotonic() + seconds + 30
        print(f"Running {label} (deadline {seconds + 30}s)", flush=True)
        response = self.api("/process", payload)
        self.save(f"{label}-result.json", response)
        if daemon:
            self.save(f"{label}-result.json", response)
            if response.get("status") not in ("running", "pending"):
                raise RuntimeError(f"{label} did not start: {response}")
            return response
        next_progress = time.monotonic() + 25
        while response.get("status") in ("running", "pending", "queued"):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                self.save(f"{label}-result.json", response)
                raise TimeoutError(f"{label} exceeded its deadline")
            time.sleep(min(5, remaining))
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(f"{label} exceeded its deadline")
            response = self.api("/process/" + payload["name"], timeout=min(30, remaining))
            self.save(f"{label}-result.json", response)
            if time.monotonic() >= next_progress:
                print(f"{label}: {response.get('status')}", flush=True)
                next_progress = time.monotonic() + 25
        self.save(f"{label}-result.json", response)
        for stream in ("stdout", "stderr"):
            (self.output / f"{label}.{stream}.txt").write_text(response.get(stream) or "")
        if response.get("exitCode") != 0 or response.get("status") not in ("completed", "exited", "finished"):
            raise RuntimeError(f"{label} failed: status={response.get('status')}, "
                               f"exitCode={response.get('exitCode')}; see {self.output}")
        self.results[label] = "passed"
        return response

    def execute(self):
        manifest = {"apiVersion": "blaxel.ai/v1alpha1", "kind": "Sandbox",
                    "metadata": {"name": self.name}, "spec": {"region": self.region,
                    "runtime": {"image": self.image, "memory": 1024,
                    "ports": [{"name": "sandbox-api", "target": 8080, "protocol": "HTTP"}],
                    "extraArgs": {"android": "enabled"}}, "volumes": [{"name": "android-storage",
                    "type": "ephemeral", "sizeMb": 8192, "mountPath": "/"}]}}
        # The CLI discovers manifests by YAML suffix; JSON is valid YAML syntax.
        manifest_file = self.save("sandbox.yaml", manifest)
        summary = {"sandbox": self.name, "workspace": self.workspace, "environment": "dev",
                   "image": self.image, "checks": self.results, "status": "failed"}
        attempted = False
        error = None
        with tempfile.TemporaryDirectory(prefix="android-e2e-cwd-") as cwd:
            self.cwd = cwd
            try:
                attempted = True  # A timed-out apply may still have created the resource.
                created = self.cli("apply", "--env-file", "/dev/null", "-f", str(manifest_file), timeout=90)
                self.save("create.json", created)
                if not isinstance(created, dict) or not created.get("applied"):
                    raise RuntimeError("bl apply did not apply any sandbox resource")
                self.wait_health()
                identity = self.cli("get", "sandbox", self.name)
                self.save("identity.json", identity)
                resources = identity if isinstance(identity, list) else [identity]
                resource = next((r for r in resources if r.get("metadata", {}).get("name") == self.name), None)
                if not resource or resource.get("spec", {}).get("runtime", {}).get("image") != self.image:
                    raise RuntimeError("Deployed sandbox image does not match requested image")
                for name in ("ready", "app-prep", "markor", "proof", "security"):
                    self.fixture(name)
                screenshot = self.api("/filesystem//work/android/verified-note.png.b64")
                (self.output / "verified-note.png").write_bytes(base64.b64decode(screenshot["content"], validate=False))
                self.fixture("restart-stop")
                self.fixture("restart-start", daemon=True)
                self.fixture("restart-ready")
                self.fixture("security", label="security-after-restart")
                summary["status"] = "passed"
            except (Exception, KeyboardInterrupt) as caught:
                error = caught
                summary["error"] = str(caught)
            finally:
                if attempted:
                    print(f"Deleting disposable sandbox {self.name}", flush=True)
                    try:
                        self.save("delete.json", self.cli("delete", "sandbox", self.name, timeout=60))
                        summary["cleanup"] = "passed"
                    except Exception as cleanup_error:
                        summary["cleanup"] = str(cleanup_error)
                        summary["status"] = "failed"
                        error = RuntimeError(f"{error or 'Checks passed'}; cleanup failed: {cleanup_error}")
                self.save("summary.json", summary)
        if error:
            raise error
        return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True)
    parser.add_argument("--workspace", default="chris")
    parser.add_argument("--region", default="us-was-1")
    parser.add_argument("--output-dir")
    args = parser.parse_args()
    try:
        runner = Runner(**vars(args))
        print(f"Dev Android E2E evidence: {runner.output}", flush=True)
        runner.execute()
        print(f"Android E2E passed. Evidence: {runner.output}")
        return 0
    except (Exception, KeyboardInterrupt) as error:
        print(f"Android E2E failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
