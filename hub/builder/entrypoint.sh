#!/bin/sh
set -e

# buildkitd needs cgroup2 for the RUN steps. Same trick as
# hub/docker-in-sandbox, which is the proof runc works inside our microVMs.
mkdir -p /sys/fs/cgroup
mount -t cgroup2 none /sys/fs/cgroup 2>/dev/null || true

# The native snapshotter copies instead of overlaying. This is not a tuning
# choice: the sandbox rootfs is already a read-only EROFS with a RAM overlay, so
# overlayfs-on-overlayfs is out — the same reason hub/docker-in-sandbox pins
# dockerd to storage-driver vfs.
#
# --root on /scratch keeps every byte buildkit writes on the ephemeral volume.
# It matters: the native snapshotter used 4GB of real disk for a 411MB image.
# If /scratch is missing the build would silently fill the RAM overlay instead,
# so fail loudly here rather than OOM mid-build.
if [ ! -d /scratch ]; then
  echo "FATAL: /scratch is not mounted. The builder needs a disk-backed" >&2
  echo "ephemeral volume; without it buildkit writes to the RAM overlay." >&2
  echo "Note ephemeral volumes are mk3.1-only: on mk3.0 the API accepts the" >&2
  echo "attachment and silently ignores it." >&2
  exit 1
fi

mkdir -p /scratch/buildkit

/usr/local/bin/sandbox-api &
API_PID=$!

# Wait for the API before registering anything against it. A build sandbox is
# created and then driven over HTTP, so the API is what has to survive here.
i=0
until curl -sf -o /dev/null http://localhost:8080/health; do
  i=$((i + 1))
  if [ "$i" -gt 300 ]; then
    echo "FATAL: the sandbox API never came up" >&2
    exit 1
  fi
  # Nothing to serve yet if the API died on its own.
  kill -0 "$API_PID" 2>/dev/null || { echo "FATAL: the sandbox API exited" >&2; exit 1; }
  sleep 0.1
done

# buildkitd runs as a process the API owns rather than as this script's exec
# target, and that is the whole point: scale-to-zero pauses a sandbox with no
# running process, and it pauses it about twenty seconds after boot. A paused
# sandbox answers 502 through the gateway instead of resuming, so the controlplane
# never saw a healthy build environment and every build failed on readiness.
#
# keepAlive is what disables scale-to-zero, and it defaults to false here despite
# what the controlplane-side model documents. timeout 0 means no auto-kill: with
# keepAlive and no timeout the API would substitute 600s and kill buildkitd ten
# minutes into a build.
curl -sf -X POST http://localhost:8080/process \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "buildkitd",
    "command": "buildkitd --root /scratch/buildkit --oci-worker-snapshotter=native --addr unix:///run/buildkit/buildkitd.sock",
    "waitForCompletion": false,
    "keepAlive": true,
    "timeout": 0
  }' >/dev/null

# PID 1 stays the API: it is what the platform probes and what the build is
# driven through. buildkitd dying is reported through the process endpoint.
wait "$API_PID"
