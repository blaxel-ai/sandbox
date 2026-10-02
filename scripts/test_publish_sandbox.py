"""Exercise the CI publisher with real jq/curl and a local HTTP receiver."""

import json
import os
from pathlib import Path
from queue import Queue
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


ROOT = Path(__file__).resolve().parents[1]


class PublishSandboxTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.requests = Queue()
        requests = cls.requests

        class Receiver(BaseHTTPRequestHandler):
            def do_PUT(self):
                payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                requests.put((self.path, self.headers["Content-Type"], payload))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"{}")

            def log_message(self, *_args):
                pass

        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Receiver)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def publish(self, template, name="test-template", expect_error=False):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "hub" / name / "template.json"
            path.parent.mkdir(parents=True)
            original = json.dumps(template)
            path.write_text(original)
            env = {
                **os.environ,
                "SANDBOX_NAME": name,
                "TAG": "test-tag",
                "BL_ENV": "dev",
                "BL_API_URL": f"http://127.0.0.1:{self.server.server_port}",
                "BL_ADMIN_USERNAME": "local-test",
                "BL_ADMIN_PASSWORD": "local-test",
                "NO_PROXY": "127.0.0.1",
                "no_proxy": "127.0.0.1",
            }
            result = subprocess.run(
                ["bash", str(ROOT / "scripts/publish-sandbox.sh")],
                cwd=directory, env=env, capture_output=True, text=True, timeout=15,
            )
            if expect_error:
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("non-empty icon", result.stderr)
                self.assertTrue(self.requests.empty(), "Invalid icons must not reach the API")
                return None
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            request_path, content_type, payload = self.requests.get(timeout=2)
            self.assertEqual(request_path, f"/admin/store/sandboxes/{name}")
            self.assertEqual(content_type, "application/json")
            self.assertEqual(payload["image"], f"blaxel/{name}:test-tag")
            for field in ("iconLight", "iconDark"):
                self.assertIsInstance(payload[field], str)
                self.assertTrue(payload[field].strip())
            self.assertEqual(path.read_text(), original)
            for key, value in template.items():
                if key not in ("iconLight", "iconDark", "image"):
                    self.assertEqual(payload[key], value)
            return payload

    def test_legacy_icon_supplies_both_themes(self):
        result = self.publish({"icon": "https://example.test/icon.svg"})
        self.assertEqual(result["iconLight"], result["icon"])
        self.assertEqual(result["iconDark"], result["icon"])

    def test_explicit_variants_are_preserved(self):
        template = {"icon": "legacy.svg", "iconLight": "light.svg", "iconDark": "dark.svg"}
        result = self.publish(template)
        self.assertEqual(result["iconLight"], "light.svg")
        self.assertEqual(result["iconDark"], "dark.svg")

    def test_dark_falls_back_to_light(self):
        for dark in (None, ""):
            with self.subTest(dark=dark):
                result = self.publish({"icon": "legacy.svg", "iconLight": "light.svg", "iconDark": dark})
                self.assertEqual(result["iconDark"], "light.svg")
        result = self.publish({"icon": "legacy.svg", "iconLight": "light.svg"})
        self.assertEqual(result["iconDark"], "light.svg")

    def test_empty_light_falls_back_to_legacy(self):
        for light in (None, ""):
            with self.subTest(light=light):
                result = self.publish({"icon": "legacy.svg", "iconLight": light, "iconDark": "dark.svg"})
                self.assertEqual(result["iconLight"], "legacy.svg")
                self.assertEqual(result["iconDark"], "dark.svg")

    def test_whitespace_variants_fall_back_to_trimmed_legacy(self):
        result = self.publish({"icon": " legacy.svg ", "iconLight": " \t", "iconDark": "\n"})
        self.assertEqual(result["iconLight"], "legacy.svg")
        self.assertEqual(result["iconDark"], "legacy.svg")

    def test_single_variant_supplies_both_themes(self):
        for field in ("iconLight", "iconDark"):
            with self.subTest(field=field):
                result = self.publish({field: "variant.svg"})
                self.assertEqual(result["iconLight"], "variant.svg")
                self.assertEqual(result["iconDark"], "variant.svg")

    def test_missing_or_invalid_icons_prevent_publication(self):
        for value in (None, "", " \t\n", False, 123, [], {}):
            with self.subTest(value=value):
                self.publish({"icon": value, "iconLight": value, "iconDark": value}, expect_error=True)
        self.publish({}, expect_error=True)

    def test_all_hub_templates_publish_with_both_themes(self):
        templates = sorted((ROOT / "hub").glob("*/template.json"))
        self.assertTrue(templates)
        for path in templates:
            with self.subTest(template=path.parent.name):
                template = json.loads(path.read_text())
                result = self.publish(template, path.parent.name)
                light = template.get("iconLight") or template["icon"]
                self.assertEqual(result["iconLight"], light)
                self.assertEqual(result["iconDark"], template.get("iconDark") or light)


if __name__ == "__main__":
    unittest.main()
