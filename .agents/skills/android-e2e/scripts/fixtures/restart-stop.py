import os, signal, time, json, pathlib, hashlib

assert {"type": "cgroup"} in json.loads(
    pathlib.Path("/opt/android/config.json").read_text()
)["linux"]["namespaces"]
files = [
    pathlib.Path("/root/.android/adbkey"),
    pathlib.Path("/var/lib/android/data/media/0/Documents/markor/QuickNote.md"),
]
baseline = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
pathlib.Path("/tmp/android-e2e-baseline.json").write_text(json.dumps(baseline))
ids = []
for p in pathlib.Path("/proc").iterdir():
    if p.name.isdigit():
        try:
            args = (p / "cmdline").read_bytes().split(b"\0")
            if len(args) > 1 and args[1] == b"/opt/android/android.py":
                ids.append(int(p.name))
        except (FileNotFoundError, PermissionError):
            pass
assert len(ids) == 1, ids
print("STOP_SUPERVISOR", ids[0], flush=True)
os.kill(ids[0], signal.SIGTERM)
for _ in range(45):
    if not pathlib.Path("/proc/" + str(ids[0])).exists():
        break
    time.sleep(1)
else:
    raise RuntimeError("Supervisor did not exit")
assert not pathlib.Path("/run/android/ready").exists()
print("SUPERVISOR_STOPPED_DATA_KEYS_PRESERVED")
