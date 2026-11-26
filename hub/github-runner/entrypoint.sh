#!/bin/sh
# Starts the Docker engine in the background, then the sandbox API in the
# foreground. Docker keeps its data on the runner volume (/runner) when the
# sandbox has one, so images and build cache survive from job to job.

export PATH="/usr/local/bin:$PATH"

start_docker() {
  echo 1 >/proc/sys/net/ipv4/ip_forward 2>/dev/null || true
  sysctl -w net.ipv6.conf.default.forwarding=1 net.ipv6.conf.all.forwarding=1 >/dev/null 2>&1 || true
  mkdir -p /sys/fs/cgroup
  mount -t cgroup2 none /sys/fs/cgroup 2>/dev/null || true

  data_root=/var/lib/docker
  if [ -d /runner ]; then
    data_root=/runner/docker
  fi
  mkdir -p "$data_root" /etc/docker

  # Overlay mounts cannot stack on the root overlay, so without the runner
  # volume Docker falls back to the slower vfs driver.
  storage=""
  case "$(stat -f -c %T "$data_root" 2>/dev/null)" in
    overlay*) storage='"storage-driver": "vfs", "features": {"containerd-snapshotter": false},' ;;
  esac

  # MTU 1280 matches the sandbox network. Containers get an IPv6 subnet from
  # the documentation prefix, masqueraded to the sandbox's address.
  cat >/etc/docker/daemon.json <<EOF
{
  $storage
  "data-root": "$data_root",
  "mtu": 1280,
  "ipv6": true,
  "ip6tables": true,
  "fixed-cidr-v6": "2001:db8:1::/64",
  "default-network-opts": {
    "bridge": {
      "com.docker.network.enable_ipv6": "true",
      "com.docker.network.driver.mtu": "1280"
    }
  }
}
EOF
  ip6tables -t nat -C POSTROUTING -s 2001:db8:1::/64 ! -o docker0 -j MASQUERADE 2>/dev/null ||
    ip6tables -t nat -A POSTROUTING -s 2001:db8:1::/64 ! -o docker0 -j MASQUERADE 2>/dev/null || true

  # Restart the engine if it stops, so one crash does not leave every later
  # job without Docker.
  while true; do
    dockerd >>/var/log/dockerd.log 2>&1
    echo "dockerd exited with $?, restarting" >>/var/log/dockerd.log
    sleep 2
  done
}

start_docker &

exec /usr/local/bin/sandbox-api
