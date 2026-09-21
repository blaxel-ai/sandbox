import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("android_e2e_runner", Path(__file__).resolve().parents[1] / "scripts/run.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.runner = MODULE.Runner("blaxel/android:develop-abcdef0", output_dir=self.directory.name)

    def test_rejects_mutable_or_production_images(self):
        for image in ("blaxel/android:latest", "blaxel/android:develop", "blaxel/android:v1", "other:develop-abcdef0"):
            with self.subTest(image=image), self.assertRaises(ValueError):
                MODULE.Runner(image)

    def test_cli_forces_dev_and_never_uses_shell(self):
        completed = subprocess.CompletedProcess([], 0, '{"ok":true}', '')
        with patch.object(MODULE.subprocess, "run", return_value=completed) as call:
            self.runner.api("/health")
        args, kwargs = call.call_args
        self.assertIsInstance(args[0], list)
        self.assertEqual(kwargs["env"]["BL_ENV"], "dev")
        self.assertIn("/dev/null", args[0])
        self.assertFalse(kwargs.get("shell", False))
        self.assertGreater(kwargs["timeout"], 0)

    def test_uncertain_creation_still_cleans_only_unique_resource(self):
        calls = []
        def cli(*args, **kwargs):
            calls.append(args)
            if args[0] == "apply":
                raise subprocess.TimeoutExpired("bl apply", 90)
            return {"deleted": True}
        with patch.object(self.runner, "cli", side_effect=cli), self.assertRaises(subprocess.TimeoutExpired):
            self.runner.execute()
        self.assertEqual(calls[-1], ("delete", "sandbox", self.runner.name))
        self.assertTrue(self.runner.name.startswith("android-e2e-"))
        summary = json.loads((self.runner.output / "summary.json").read_text())
        self.assertEqual(summary["status"], "failed")
        self.assertEqual(summary["cleanup"], "passed")

    def test_cleanup_failure_is_not_hidden(self):
        with patch.object(self.runner, "cli", side_effect=RuntimeError("unavailable")):
            with self.assertRaisesRegex(RuntimeError, "cleanup failed"):
                self.runner.execute()
        summary = json.loads((self.runner.output / "summary.json").read_text())
        self.assertEqual(summary["status"], "failed")
        self.assertIn("unavailable", summary["cleanup"])

    def test_empty_apply_result_fails_before_health_polling(self):
        with patch.object(self.runner, "cli", return_value={"success": True, "applied": []}), \
             patch.object(self.runner, "wait_health") as health:
            with self.assertRaisesRegex(RuntimeError, "did not apply"):
                self.runner.execute()
        health.assert_not_called()
        self.assertTrue((self.runner.output / "sandbox.yaml").exists())

    def test_json_failure_is_rejected_even_with_zero_cli_exit(self):
        for response in ({"success": False}, {"success": True, "failed": ["sandbox"]}):
            completed = subprocess.CompletedProcess([], 0, json.dumps(response), "")
            with patch.object(MODULE.subprocess, "run", return_value=completed):
                with self.assertRaisesRegex(RuntimeError, "returned failure"):
                    self.runner.cli("apply")

    def test_health_retries_unhealthy_json(self):
        with patch.object(self.runner, "api", side_effect=[{"status": "unhealthy"}, {"status": "ok"}]) as api, \
             patch.object(MODULE.time, "sleep"):
            self.runner.wait_health()
        self.assertEqual(api.call_count, 2)

    def test_wrong_deployed_image_fails_and_cleans_up(self):
        calls = []
        def cli(*args, **kwargs):
            calls.append(args)
            if args[0] == "get":
                return [{"metadata": {"name": self.runner.name}, "spec": {"runtime": {"image": "wrong"}}}]
            return {"applied": [{"name": self.runner.name}]}
        with patch.object(self.runner, "cli", side_effect=cli), patch.object(self.runner, "wait_health"):
            with self.assertRaisesRegex(RuntimeError, "image does not match"):
                self.runner.execute()
        self.assertEqual(calls[-1], ("delete", "sandbox", self.runner.name))

    def test_process_deadline_is_bounded_even_when_remote_stays_running(self):
        with patch.object(self.runner, "api", return_value={"status": "running"}), \
             patch.object(MODULE.time, "monotonic", side_effect=[0, 0, 10000]):
            with self.assertRaisesRegex(TimeoutError, "deadline"):
                self.runner.fixture("security")

    def test_failed_process_preserves_logs(self):
        result = {"status": "failed", "exitCode": 1, "stdout": "context", "stderr": "assertion"}
        with patch.object(self.runner, "api", return_value=result):
            with self.assertRaisesRegex(RuntimeError, "security failed"):
                self.runner.fixture("security")
        self.assertEqual((self.runner.output / "security.stderr.txt").read_text(), "assertion")
        self.assertEqual((self.runner.output / "security.stdout.txt").read_text(), "context")


if __name__ == "__main__":
    unittest.main()
