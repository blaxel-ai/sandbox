#!/usr/bin/env python3
"""Smoke-test the published Node-based hub templates against a live Blaxel workspace.

For every Node template this script spins up a throwaway sandbox from
``blaxel/<template>:latest`` and runs a mode-appropriate check:

  - node:       run ``node --version`` and assert the major matches the target
  - preview:    let the template auto-start its dev server, open a public preview
                URL and assert it answers HTTP 200 (end-to-end framework check)
  - playwright: run the image's bundled sample-test.js, which drives a real
                browser through the Playwright server, and assert it passes

Every template also reports its detected Node version. Failures are isolated per
template, and each sandbox is deleted at the end of its run.

Usage:
    BL_WORKSPACE=main python3 hub/node-test.py                 # test every template
    BL_WORKSPACE=main python3 hub/node-test.py nextjs vite     # test a subset

Environment variables:
    BL_WORKSPACE          Blaxel workspace to create the sandboxes in (required).
    EXPECTED_NODE_MAJOR   Node major version to assert (default: "24").
    SKIP_VERSION_CHECK    Set to "1" to skip the Node major assertion (useful for
                          exercising the harness against pre-release images).

Requires the Blaxel Python SDK (``pip install blaxel``) and a logged-in CLI
(``bl login <workspace>``) or BL_API_KEY in the environment.
"""
import asyncio
import os
import sys
import urllib.request

from blaxel.core import SandboxInstance

EXPECTED_MAJOR = os.environ.get("EXPECTED_NODE_MAJOR", "24")
SKIP_VERSION_CHECK = os.environ.get("SKIP_VERSION_CHECK") == "1"

# Node-based hub templates and how to verify each one. "boot" is the max number
# of seconds we poll a preview URL before giving up (dev servers vary widely:
# Vite/Astro boot fast, Next.js compiles on first hit, Expo's Metro is slowest).
TEMPLATES = [
    {"name": "base-image", "mode": "node", "memory": 2048},
    {"name": "node", "mode": "node", "memory": 2048},
    {"name": "node-slim", "mode": "node", "memory": 2048},
    {"name": "ts-app", "mode": "node", "memory": 2048},
    {"name": "app-runner", "mode": "node", "memory": 2048},
    {"name": "nextjs", "mode": "preview", "port": 3000, "memory": 4096, "boot": 240},
    {"name": "vite", "mode": "preview", "port": 5173, "memory": 4096, "boot": 180},
    {"name": "astro", "mode": "preview", "port": 4321, "memory": 4096, "boot": 180},
    {"name": "expo", "mode": "preview", "port": 8081, "memory": 8192, "boot": 300},
    {"name": "playwright-chromium", "mode": "playwright", "memory": 8192},
    {"name": "playwright-firefox", "mode": "playwright", "memory": 8192},
]


def http_get(url: str, timeout: int = 15):
    req = urllib.request.Request(url, headers={"User-Agent": "hub-node-test/1.0"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return resp.status, resp.read(400).decode("utf-8", "replace")


async def get_node_version(sandbox) -> str:
    proc = await sandbox.process.exec({
        "name": "node-version",
        "command": "node --version",
        "wait_for_completion": True,
        "timeout": 30000,
    })
    return (proc.logs or "").strip().splitlines()[0].strip() if proc.logs else ""


async def poll_preview(url: str, boot: int) -> tuple[bool, str]:
    waited = 0
    last = ""
    while waited < boot:
        try:
            status, body = http_get(url, timeout=15)
            last = f"HTTP {status}"
            if status == 200:
                return True, f"HTTP 200 ({len(body)}+ bytes)"
        except Exception as e:  # noqa: BLE001
            last = f"{type(e).__name__}: {e}"
        await asyncio.sleep(5)
        waited += 5
    return False, f"no 200 within {boot}s (last: {last})"


async def verify(tpl: dict) -> dict:
    name = f"node-test-{tpl['name']}"
    mode = tpl["mode"]
    sandbox = None
    res = {"template": tpl["name"], "mode": mode, "ok": False, "node": "", "detail": ""}
    try:
        create_spec = {
            "name": name,
            "image": f"blaxel/{tpl['name']}:latest",
            "memory": tpl.get("memory", 4096),
        }
        if mode == "preview":
            create_spec["ports"] = [{"target": tpl["port"], "protocol": "HTTP"}]
        sandbox = await SandboxInstance.create_if_not_exists(create_spec)
        await sandbox.wait(max_wait=180000, interval=2000)

        res["node"] = await get_node_version(sandbox)
        node_ok = SKIP_VERSION_CHECK or res["node"].lstrip("v").split(".")[0] == EXPECTED_MAJOR

        if mode == "node":
            res["ok"] = node_ok
            res["detail"] = res["node"] or "no node output"

        elif mode == "preview":
            preview = await sandbox.previews.create_if_not_exists({
                "metadata": {"name": "node-test-preview"},
                "spec": {"port": tpl["port"], "public": True},
            })
            ok, detail = await poll_preview(preview.spec.url, tpl.get("boot", 180))
            res["ok"] = ok and node_ok
            res["detail"] = f"{detail} | {preview.spec.url}"

        elif mode == "playwright":
            proc = await sandbox.process.exec({
                "name": "pw-test",
                "command": "node /home/playwright/sample-test.js",
                "wait_for_completion": True,
                "timeout": 120000,
            })
            logs = proc.logs or ""
            res["ok"] = ("All tests passed!" in logs) and node_ok
            res["detail"] = " | ".join(l.strip() for l in logs.splitlines() if l.strip()) or "no output"

        return res
    except Exception as e:  # noqa: BLE001
        res["detail"] = f"{type(e).__name__}: {e}"
        return res
    finally:
        if sandbox is not None:
            try:
                await sandbox.delete()
            except Exception:  # noqa: BLE001
                pass


async def main():
    if not os.environ.get("BL_WORKSPACE"):
        print("BL_WORKSPACE is not set; aborting.", file=sys.stderr)
        sys.exit(2)

    only = sys.argv[1:]
    targets = [t for t in TEMPLATES if not only or t["name"] in only]
    if not targets:
        print(f"No matching templates for: {', '.join(only)}", file=sys.stderr)
        sys.exit(2)

    label = "any" if SKIP_VERSION_CHECK else EXPECTED_MAJOR
    print(f"Testing {len(targets)} hub templates (expected Node major: {label})\n")

    results = []
    for t in targets:
        print(f"-> {t['name']} ({t['mode']}) ...", flush=True)
        r = await verify(t)
        results.append(r)
        status = "PASS" if r["ok"] else "FAIL"
        print(f"   [{status}] node={r['node'] or '?'} :: {r['detail'][:200]}\n", flush=True)

    print("=" * 70)
    print("SUMMARY")
    print("=" * 70)
    for r in results:
        status = "PASS" if r["ok"] else "FAIL"
        print(f"  [{status}] {r['template']:<22} {r['mode']:<10} node={r['node'] or '?':<10} {r['detail'][:70]}")
    passed = [r for r in results if r["ok"]]
    print(f"\n{len(passed)}/{len(results)} templates passed")
    failed = [r for r in results if not r["ok"]]
    if failed:
        print("FAILED:", ", ".join(r["template"] for r in failed))
        sys.exit(1)


if __name__ == "__main__":
    asyncio.run(main())
