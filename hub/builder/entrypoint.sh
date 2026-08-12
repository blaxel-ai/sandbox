#!/bin/sh
set -e

/usr/local/bin/sandbox-api &

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
exec buildkitd \
  --root /scratch/buildkit \
  --oci-worker-snapshotter=native \
  --addr unix:///run/buildkit/buildkitd.sock
