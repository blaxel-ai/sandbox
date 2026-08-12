#!/bin/sh
set -eu

# Resolve the production entrypoint relative to this file so any working directory works.
entrypoint=$(CDPATH= cd -- "$(dirname "$0")" && pwd)/entrypoint.sh
# Keep fake binaries and PID files isolated, then remove them on every exit path.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Multi-process wait supervision requires Bash rather than BusyBox ash.
if [ "$(head -n 1 "$entrypoint")" != '#!/bin/bash' ]; then
  echo "entrypoint must run with Bash" >&2
  exit 1
fi

# A sandbox cannot join a tailnet without an auth key, so fail before starting services.
if output=$(env -u TS_AUTHKEY bash "$entrypoint" 2>&1); then
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

PATH="$tmp:$PATH" TS_AUTHKEY=test BL_NAME=test-sandbox bash "$entrypoint"

# If containerboot exits before Tailscale is ready, surface that failure instead of waiting.
cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
exit 1
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "NeedsLogin"}'
EOF
if output=$(PATH="$tmp:$PATH" TS_AUTHKEY=test bash "$entrypoint" 2>&1); then
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
if output=$(PATH="$tmp:$PATH" TS_AUTHKEY=test TS_STARTUP_TIMEOUT_SECONDS=0 bash "$entrypoint" 2>&1); then
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
TEST_CHILD_PID_FILE="$tmp/child.pid" PATH="$tmp:$PATH" TS_AUTHKEY=test bash "$entrypoint" &
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
exit 23
EOF
cat >"$tmp/tailscale" <<'EOF'
#!/bin/sh
printf '%s\n' '{"BackendState": "Running"}'
EOF
cat >"$tmp/sandbox-api" <<'EOF'
#!/bin/sh
echo $$ >"$TEST_SURVIVOR_PID_FILE"
exec sleep 10
EOF
TEST_SURVIVOR_PID_FILE="$tmp/sandbox-api.pid" PATH="$tmp:$PATH" TS_AUTHKEY=test bash "$entrypoint" &
entrypoint_pid=$!
attempts=0
while kill -0 "$entrypoint_pid" 2>/dev/null && [ "$attempts" -lt 4 ]; do
  attempts=$((attempts + 1))
  sleep 1
done
if kill -0 "$entrypoint_pid" 2>/dev/null; then
  kill "$entrypoint_pid" 2>/dev/null || true
  wait "$entrypoint_pid" 2>/dev/null || true
  echo "entrypoint waited for sandbox-api after Tailscale failed" >&2
  exit 1
fi
set +e
wait "$entrypoint_pid"
status=$?
set -e
if [ "$status" -ne 23 ]; then
  echo "entrypoint returned $status instead of Tailscale status 23" >&2
  exit 1
fi
if kill -0 "$(cat "$tmp/sandbox-api.pid")" 2>/dev/null; then
  echo "entrypoint left sandbox-api running after Tailscale failed" >&2
  exit 1
fi

# The same supervision rule applies when sandbox-api exits before Tailscale.
cat >"$tmp/containerboot" <<'EOF'
#!/bin/sh
echo $$ >"$TEST_SURVIVOR_PID_FILE"
exec sleep 10
EOF
cat >"$tmp/sandbox-api" <<'EOF'
#!/bin/sh
sleep 1
exit 24
EOF
TEST_SURVIVOR_PID_FILE="$tmp/containerboot.pid" PATH="$tmp:$PATH" TS_AUTHKEY=test bash "$entrypoint" &
entrypoint_pid=$!
attempts=0
while kill -0 "$entrypoint_pid" 2>/dev/null && [ "$attempts" -lt 4 ]; do
  attempts=$((attempts + 1))
  sleep 1
done
if kill -0 "$entrypoint_pid" 2>/dev/null; then
  kill "$entrypoint_pid" 2>/dev/null || true
  wait "$entrypoint_pid" 2>/dev/null || true
  echo "entrypoint waited for Tailscale after sandbox-api failed" >&2
  exit 1
fi
set +e
wait "$entrypoint_pid"
status=$?
set -e
if [ "$status" -ne 24 ]; then
  echo "entrypoint returned $status instead of sandbox-api status 24" >&2
  exit 1
fi
if kill -0 "$(cat "$tmp/containerboot.pid")" 2>/dev/null; then
  echo "entrypoint left Tailscale running after sandbox-api failed" >&2
  exit 1
fi
