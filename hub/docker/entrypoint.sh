#!/bin/sh

# Set environment variables
export PATH="/usr/local/bin:$PATH"
export SHELL=/bin/zsh

# NOTE: Known limitations of Docker in this sandbox environment:
#   - "docker exec" does not work (the sandbox kernel does not support the
#     namespace operations runc needs for exec). Use "docker run" or
#     "docker logs" / "docker attach" instead.
#   - Healthchecks (CMD-SHELL) do not work for the same reason. Build retry
#     logic into your apps instead of relying on depends_on + service_healthy.
#   - The sandbox kernel lacks the raw iptables table, so direct access
#     filtering is disabled via DOCKER_INSECURE_NO_IPTABLES_RAW.

export DOCKER_INSECURE_NO_IPTABLES_RAW=1

# Start sandbox-api in the background
/usr/local/bin/sandbox-api &

# Enable IPv4 forwarding at the kernel level
echo 1 > /proc/sys/net/ipv4/ip_forward

# Setup Docker
mkdir -p /sys/fs/cgroup
mount -t cgroup2 none /sys/fs/cgroup 2>/dev/null || true
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'JSON'
{
  "storage-driver": "vfs"
}
JSON

dockerd --config-file=/etc/docker/daemon.json --log-level=debug --host=unix:///run/docker.sock