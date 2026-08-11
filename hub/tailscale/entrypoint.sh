#!/bin/sh
set -eu

if [ -z "${TS_AUTHKEY:-}" ]; then
  echo "TS_AUTHKEY is required to connect this sandbox to Tailscale" >&2
  exit 1
fi

export TS_HOSTNAME="${TS_HOSTNAME:-${SANDBOX_NAME:-${BL_NAME:-tailscale-sandbox}}}"

containerboot &
containerboot_pid=$!

until tailscale --socket="${TS_SOCKET:-/tmp/tailscaled.sock}" status --json 2>/dev/null | grep -q '"BackendState": *"Running"'; do
  if ! kill -0 "$containerboot_pid" 2>/dev/null; then
    echo "Tailscale stopped before connecting this sandbox" >&2
    wait "$containerboot_pid"
    exit 1
  fi
  sleep 1
done

exec sandbox-api
