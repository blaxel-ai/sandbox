import pathlib, json, time, hashlib, subprocess

for _ in range(25):
    p = pathlib.Path("/run/android/status.json")
    state = json.loads(p.read_text()) if p.exists() else {}
    if state.get("state") == "ready":
        break
    if state.get("state") == "failed":
        raise RuntimeError(state)
    time.sleep(2)
else:
    raise RuntimeError("Readiness timeout")
for name, digest in json.loads(
    pathlib.Path("/tmp/android-e2e-baseline.json").read_text()
).items():
    assert hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest() == digest, name
print("READY_DATA_KEYS_PRESERVED", state)
print(
    subprocess.check_output(
        ["runc", "--root", "/run/android/runc", "state", "android"], text=True
    )
)
