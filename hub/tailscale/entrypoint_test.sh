#!/bin/sh
set -eu

# Resolve the production entrypoint relative to this file so any working directory works.
entrypoint=$(CDPATH= cd -- "$(dirname "$0")" && pwd)/entrypoint.sh
# Keep fake binaries and PID files isolated, then remove them on every exit path.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# A sandbox cannot join a tailnet without an auth key, so fail before starting services.
if output=$(env -u TS_AUTHKEY sh "$entrypoint" 2>&1); then
  echo "entrypoint accepted a missing TS_AUTHKEY" >&2
  exit 1
fi
case "$output" in
  *"TS_AUTHKEY is required"*) ;;
  *) echo "entrypoint did not explain the missing TS_AUTHKEY" >&2; exit 1 ;;
esac

# Shadow the real binaries through PATH so the entrypoint can be tested without Tailscale.
# This happy path also proves that BL_NAME becomes the default Tailscale hostname.
cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
sleep 1
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "Running"}'
EOF
cat >"$tmp/sandbox-api" <<'EOF'
#!/bin/sh
test "$TS_HOSTNAME" = "test-sandbox"
EOF
chmod +x "$tmp/containerboot" "$tmp/tailscale" "$tmp/sandbox-api"

PATH="$tmp:$PATH" TS_AUTHKEY=test BL_NAME=test-sandbox sh "$entrypoint"

# If containerboot exits before Tailscale is ready, surface that failure instead of waiting.
cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
exit 1
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "NeedsLogin"}'
EOF
if output=$(PATH="$tmp:$PATH" TS_AUTHKEY=test sh "$entrypoint" 2>&1); then
  echo "entrypoint ignored containerboot failure" >&2
  exit 1
fi
case "$output" in
  *"Tailscale stopped before connecting"*) ;;
  *) echo "entrypoint did not explain containerboot failure" >&2; exit 1 ;;
esac

# Bound startup when containerboot stays alive but Tailscale never reaches Running.
cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
sleep 5
EOF
if output=$(PATH="$tmp:$PATH" TS_AUTHKEY=test TS_STARTUP_TIMEOUT_SECONDS=0 sh "$entrypoint" 2>&1); then
  echo "entrypoint ignored the startup timeout" >&2
  exit 1
fi
case "$output" in
  *"Tailscale did not connect within 0 seconds"*) ;;
  *) echo "entrypoint did not explain the startup timeout" >&2; exit 1 ;;
esac

# A termination signal during startup must reach containerboot and leave no child behind.
cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
echo $$ >"$TEST_CHILD_PID_FILE"
sleep 5
EOF
TEST_CHILD_PID_FILE="$tmp/child.pid" PATH="$tmp:$PATH" TS_AUTHKEY=test sh "$entrypoint" &
entrypoint_pid=$!
attempts=0
# Wait only long enough to prove containerboot started; never let a failed test hang.
while [ ! -s "$tmp/child.pid" ]; do
  if ! kill -0 "$entrypoint_pid" 2>/dev/null || [ "$attempts" -ge 5 ]; then
    kill "$entrypoint_pid" 2>/dev/null || true
    wait "$entrypoint_pid" 2>/dev/null || true
    echo "entrypoint did not start containerboot" >&2
    exit 1
  fi
  attempts=$((attempts + 1))
  sleep 1
done
# Signal the supervisor, then verify both its exit status and child cleanup.
kill -TERM "$entrypoint_pid"
if wait "$entrypoint_pid"; then
  echo "entrypoint ignored a startup termination signal" >&2
  exit 1
fi
if kill -0 "$(cat "$tmp/child.pid")" 2>/dev/null; then
  echo "entrypoint left containerboot running after termination" >&2
  exit 1
fi

# Tailscale is still required after readiness; its later failure must fail the entrypoint.
cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
sleep 1
exit 1
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "Running"}'
EOF
cat >"$tmp/sandbox-api" <<'EOF'
#!/bin/sh
sleep 5
EOF
if PATH="$tmp:$PATH" TS_AUTHKEY=test sh "$entrypoint"; then
  echo "entrypoint ignored post-readiness Tailscale failure" >&2
  exit 1
fi
