#!/bin/sh
set -e

# buildkitd needs cgroup2 for the RUN steps. Same trick as
# hub/docker-in-sandbox, which is the proof runc works inside our microVMs.
mkdir -p /sys/fs/cgroup
mount -t cgroup2 none /sys/fs/cgroup 2>/dev/null || true

# overlayfs, not native. The earlier reasoning was that the sandbox rootfs is a
# read-only EROFS under a RAM overlay, so overlayfs-on-overlayfs is out. True of
# `/`, but buildkit never writes there: --root puts it on /scratch, which is a
# tmpfs (or an ephemeral volume), and stacking on those is fine. Verified on
# 6.1.166 — the mount succeeds, buildkitd reports snapshotter:overlayfs, and the
# tree it produces is byte-for-byte the size native produced.
#
# native copies the accumulated snapshot once per layer, so a build costs the SUM
# OF PREFIXES of its base image rather than its size. On a 27-layer base that is
# an ~8x tax that Depot never pays, and it decided whether a build ran at all:
#
#   hub/nextjs      native 9 992MB of scratch   overlayfs 5 108MB   (same 1 361MiB rootfs)
#   hub/cua-xfce    native FAILED at 14GB and at 28GB   overlayfs built in 114s at 14 577MB
#
# Capping buildkit's cache does not substitute for this: --oci-worker-gc-keepstorage
# was measured at 6GB and changed nothing, because GC cannot reclaim records the
# running solve still holds. It only cleans up afterwards, which is too late.
#
# --root on /scratch keeps every byte buildkit writes off the root filesystem.
#
# The ephemeral volume is mk3.1-only, and on mk3.0 the API accepts the
# attachment and silently ignores it — no error anywhere, /scratch simply does
# not exist in the guest. This used to be fatal, on the grounds that buildkit
# would otherwise fill the RAM overlay. Refusing to start turned a degraded
# build into no build at all, so fall back to an explicit tmpfs instead: same
# memory, but sized and named rather than quietly eating the overlay.
#
# Sized at 90% of RAM. A tmpfs page is a RAM page, so the build shares its
# budget with buildkit's own processes, and headroom is what keeps an oversized
# image failing on ENOSPC — which names the problem — rather than on the OOM
# killer, which does not.
#
# It was 70%, which is where the headroom was wrong rather than merely generous:
# the amplification is ~7x the final image (measured: hub/base-image 417MiB ->
# 2 367MB of scratch, hub/nextjs 1 361MiB -> 9 956MB), so the scratch is what
# runs out first, by a wide margin. At its 9.8GB peak nextjs still left 6GB of
# RAM unused, so 30% was paying for a shortage that never happened while causing
# one that did. 90% buys 2.7GB more scratch out of the same 16GB.
if [ ! -d /scratch ]; then
  scratch_mb=$(awk '/MemTotal/ {printf "%d", $2 * 9 / 10 / 1024}' /proc/meminfo)
  echo "WARNING: /scratch is not mounted, falling back to a ${scratch_mb}MB tmpfs." >&2
  echo "Ephemeral volumes are mk3.1-only; on mk3.0 the attachment is accepted" >&2
  echo "and ignored. A large image may now exhaust memory instead of disk." >&2
  mkdir -p /scratch
  mount -t tmpfs -o "size=${scratch_mb}m" tmpfs /scratch
fi

mkdir -p /scratch/buildkit
df -h /scratch >&2

/usr/local/bin/sandbox-api &
API_PID=$!

# Wait for the API before registering anything against it. A build sandbox is
# created and then driven over HTTP, so the API is what has to survive here.
# Bounded per attempt: a refused connection returns at once, but a hung one
# would otherwise eat the whole budget in a single call.
i=0
until curl -sf -m 2 -o /dev/null http://localhost:8080/health; do
  i=$((i + 1))
  if [ "$i" -gt 600 ]; then
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
    "command": "buildkitd --root /scratch/buildkit --oci-worker-snapshotter=overlayfs --addr unix:///run/buildkit/buildkitd.sock",
    "waitForCompletion": false,
    "keepAlive": true,
    "timeout": 0
  }' >/dev/null

# PID 1 stays the API: it is what the platform probes and what the build is
# driven through. buildkitd dying is reported through the process endpoint.
wait "$API_PID"
