#!/bin/sh
# Runs one single-use GitHub Actions runner with the just-in-time
# configuration in JIT_CONFIG, and exits when its job is done.
#
# Everything that should outlive the job lives under /runner, the runner
# volume, when the sandbox has one: the work folder, the tool cache, the
# language caches and newer runner releases.
set -eu

: "${JIT_CONFIG:?JIT_CONFIG is required}"

base=/runner
[ -d "$base" ] || base=/tmp/runner
mkdir -p "$base/work" "$base/toolcache" "$base/cache" "$base/tmp"

# GitHub stops sending jobs to deprecated runner versions, so a runner newer
# than the one baked into the image is fetched once and kept on the volume.
# Any failure falls back to the baked runner.
runner_dir() {
  baked=$(cat /actions-runner/.version)
  latest=$(curl -fsSI --max-time 10 https://github.com/actions/runner/releases/latest |
    tr -d '\r' | sed -n 's#^[Ll]ocation: .*/tag/v\(.*\)$#\1#p') || latest=""
  if [ -z "$latest" ] || [ "$latest" = "$baked" ]; then
    echo /actions-runner
    return
  fi
  dir="$base/actions-runner/$latest"
  if [ ! -x "$dir/run.sh" ]; then
    arch=x64
    [ "$(uname -m)" = "aarch64" ] && arch=arm64
    tmp=$(mktemp -d "$base/tmp/runner.XXXXXX")
    if ! curl -fsSL --retry 3 --max-time 300 \
      "https://github.com/actions/runner/releases/download/v$latest/actions-runner-linux-$arch-$latest.tar.gz" |
      tar xz -C "$tmp"; then
      rm -rf "$tmp"
      echo /actions-runner
      return
    fi
    mkdir -p "$base/actions-runner"
    mv "$tmp" "$dir"
  fi
  echo "$dir"
}
dir=$(runner_dir)

# The engine starts with the sandbox; give it a moment after a cold start.
tries=0
until docker info >/dev/null 2>&1; do
  tries=$((tries + 1))
  if [ "$tries" -ge 30 ]; then
    echo "Docker is not running, see /var/log/dockerd.log" >&2
    break
  fi
  sleep 1
done

export RUNNER_ALLOW_RUNASROOT=1
export ACTIONS_RUNNER_HOOK_JOB_STARTED=/usr/local/bin/job-started
export RUNNER_TOOL_CACHE="$base/toolcache" AGENT_TOOLSDIRECTORY="$base/toolcache"
export GOCACHE="$base/cache/go-build" GOMODCACHE="$base/cache/go-mod"
export npm_config_cache="$base/cache/npm" PIP_CACHE_DIR="$base/cache/pip"

cd "$dir"
if [ ! -L _work ]; then
  rm -rf _work
  ln -s "$base/work" _work
fi
exec ./run.sh --jitconfig "$JIT_CONFIG"
