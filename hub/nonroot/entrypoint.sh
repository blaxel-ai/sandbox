#!/bin/sh
# Entrypoint for a sandbox whose workload runs unprivileged.
#
# It runs as root (PID 1), does the root-only preparation an image needs, and
# then hands the workload identity to sandbox-api, which keeps its own
# privileges (drive mounts, WireGuard, CA bundle, keep-alive) but runs every
# process, terminal and filesystem operation as that user.
#
# The image must NOT have a USER directive: that would de-privilege PID 1 —
# sandbox-api itself — and break drive mounting.
set -eu

# Docker USER syntax: "app", "10001", "app:app", "10001:10001".
SANDBOX_USER="${BL_SANDBOX_USER:-app}"

# Root-only preparation goes here, before privileges are handed over.
# Anything the workload has to write to must belong to it.
chown -R "$SANDBOX_USER" "${HOME:-/blaxel}" 2>/dev/null || true

exec /usr/local/bin/sandbox-api --user "$SANDBOX_USER" "$@"
