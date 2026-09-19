# Android sandbox

Android 12 (ReDroid), launched directly by `runc` inside the sandbox microVM. The image contains Android and ADB at build time. There is no Docker daemon, containerd, registry pull, filesystem extraction, or package installation during startup.

The template is initially hidden while kernel availability is being rolled out. The Android kernel is currently verified only on dev `us-was-1`; do not assume other regions support this image.

## Requirements

- mk3.1 with the `android` guest kernel: `spec.runtime.extraArgs.android: enabled`.
- 1,024 MB RAM minimum for the tested lightweight app scenario. Larger apps may need more.
- An 8,192 MB ephemeral volume mounted at `/`. Android needs `user.*` extended attributes, and a disk-backed root avoids putting installed files in guest RAM. This volume is not durable after sandbox deletion.
- Root execution inside the microVM. Android has broad guest capabilities, with separate PID, mount, IPC, UTS and network namespaces. The microVM remains the tenant isolation boundary.

The Hub template carries creation options for the frontend to include in the existing sandbox creation request. SDK/CLI callers must set those options themselves. No image metadata request is needed during sandbox creation.

## Readiness and agent use

The sandbox management API starts immediately while Android boots in the background. API health does not mean Android is ready. Check `/run/android/status.json` for `state: ready` before using ADB. Boot readiness is bounded to 180 seconds, plus bounded command execution time. A failure leaves a `failed` status and diagnostics accessible through the management API.

```sh
cat /run/android/status.json
export ANDROID_SERIAL="$(cat /run/android/adb-address)"
adb install /blaxel/app.apk
adb shell am start -n com.example.app/.MainActivity
adb shell input tap 360 640
adb shell input text 'Hello%sAndroid'
adb shell uiautomator dump /data/local/tmp/window.xml
adb pull /data/local/tmp/window.xml /blaxel/window.xml
adb exec-out screencap -p > /blaxel/screen.png
```

ADB is on a private veth subnet reachable only inside the sandbox. It is not published as a preview port. The launcher selects a free subnet and configures Android networking before starting `/init`.

## Diagnostics and restart

- Status: `/run/android/status.json`
- Android console: `/var/log/android/console.log`
- OCI runtime log: `/var/log/android/runc.log`
- Android data: `/var/lib/android/data`
- Android logs while running: `adb logcat -d`

Startup removes stale runtime state, interfaces and NAT rules from a previous launch. A lock prevents concurrent supervisors. SIGTERM stops Android and removes its network rules; after an uncatchable termination the next startup performs the same cleanup. A failed boot does not trigger an endless restart loop. After fixing the cause, rerun `python3 /opt/android/android.py` through the process API. If the supervisor is still running, terminate it first.

## Validation

```sh
python3 -m unittest discover -s hub/android/tests -v
docker build --platform linux/amd64 -f hub/android/Dockerfile -t blaxel/android:test .
```

A regular local Docker VM may lack the required Binder kernel support. Full startup validation requires a sandbox with the Android kernel and root volume. The daemon-free prototype was exercised on amd64 with 1 GiB RAM: Markor installation, screen interaction, note creation and persistence after app restart. Arm64 runtime behavior has not been verified. The Hub workflow currently publishes amd64 images.

ReDroid is pinned to `sha256:a6c464bbedcf1dcb67dbf91f329fbb19bee5b50631f0ca6bda6ed7c41b0e64e2`. The build keeps `slim: false` because Android loads binaries and libraries dynamically.
