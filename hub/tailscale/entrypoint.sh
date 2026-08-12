#!/bin/bash
set -eu

if [ -z "${TS_AUTHKEY:-}" ]; then
  echo "TS_AUTHKEY is required to connect this sandbox to Tailscale" >&2
  exit 1
fi

export TS_HOSTNAME="${TS_HOSTNAME:-${SANDBOX_NAME:-${BL_NAME:-tailscale-sandbox}}}"
startup_timeout=${TS_STARTUP_TIMEOUT_SECONDS:-60}
case "$startup_timeout" in
  ''|*[!0-9]*) echo "TS_STARTUP_TIMEOUT_SECONDS must be a non-negative integer" >&2; exit 1 ;;
esac

containerboot &
containerboot_pid=$!
sandbox_api_pid=

cleanup() {
  signal=${1:-TERM}
  trap - EXIT INT TERM
  kill -s "$signal" "$containerboot_pid" 2>/dev/null || true
  if [ -n "$sandbox_api_pid" ]; then
    kill -s "$signal" "$sandbox_api_pid" 2>/dev/null || true
    wait "$sandbox_api_pid" 2>/dev/null || true
  fi
  wait "$containerboot_pid" 2>/dev/null || true
}
trap 'cleanup INT; exit 130' INT
trap 'cleanup TERM; exit 143' TERM
trap cleanup EXIT

elapsed=0

until tailscale --socket="${TS_SOCKET:-/tmp/tailscaled.sock}" status --json 2>/dev/null | grep -q '"BackendState": *"Running"'; do
  if ! kill -0 "$containerboot_pid" 2>/dev/null; then
    echo "Tailscale stopped before connecting this sandbox" >&2
    wait "$containerboot_pid"
    exit 1
  fi
  if [ "$elapsed" -ge "$startup_timeout" ]; then
    echo "Tailscale did not connect within ${startup_timeout} seconds" >&2
    kill "$containerboot_pid" 2>/dev/null || true
    wait "$containerboot_pid" 2>/dev/null || true
    exit 1
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

sandbox-api &
sandbox_api_pid=$!

set +e
wait -n "$containerboot_pid" "$sandbox_api_pid"
status=$?
set -e
exit "$status"
