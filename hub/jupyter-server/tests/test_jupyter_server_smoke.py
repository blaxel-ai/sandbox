"""Smoke test for the ``jupyter-server`` bump to 2.21.0 (GHSA-c3mw-737p-c7g2,
token-bearing Referer values logged on 5xx responses).

Boots ``jupyter server`` the way ``entrypoint.sh`` does (same flags, this
image's ``jupyter_server_config.py`` through JUPYTER_CONFIG_PATH), only on a
loopback port and a temporary root dir, then drives the REST API a client of
the image uses: status, start a kernel, list it, shut it down.
"""

import os
import socket
import subprocess
import sys
import time
from pathlib import Path

import requests

CONFIG_DIR = Path(__file__).resolve().parent.parent


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def test_jupyter_server_boots_and_runs_a_kernel(tmp_path):
    port = _free_port()
    env = dict(os.environ, HOME=str(tmp_path), JUPYTER_CONFIG_PATH=str(CONFIG_DIR))
    proc = subprocess.Popen(
        [sys.executable, "-m", "jupyter_server", "--no-browser", "--ip=127.0.0.1", f"--port={port}",
         "--ServerApp.token=", "--ServerApp.password=", "--IdentityProvider.token=", "--allow-root",
         f"--ServerApp.root_dir={tmp_path}"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
    )
    base = f"http://127.0.0.1:{port}"
    try:
        deadline = time.time() + 60
        while True:
            assert proc.poll() is None, proc.stderr.read().decode()[-2000:]
            try:
                if requests.get(f"{base}/api/status", timeout=2).status_code == 200:
                    break
            except requests.ConnectionError:
                pass
            assert time.time() < deadline, "jupyter server did not come up"
            time.sleep(0.5)
        kernel = requests.post(f"{base}/api/kernels", json={"name": "python3"}, timeout=60)
        assert kernel.status_code == 201, kernel.text
        kernel_id = kernel.json()["id"]
        listed = requests.get(f"{base}/api/kernels", timeout=10).json()
        assert kernel_id in [k["id"] for k in listed]
        assert requests.delete(f"{base}/api/kernels/{kernel_id}", timeout=30).status_code == 204
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=20)
        except subprocess.TimeoutExpired:
            proc.kill()
