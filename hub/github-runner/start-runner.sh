#!/bin/sh
set -eu

: "${JIT_CONFIG:?JIT_CONFIG is required}"

base=/runner
[ -d "$base" ] || base=/tmp/runner

stop_job_processes() {
  for proc in /proc/[0-9]*; do
    pid=${proc#/proc/}
    if [ "$pid" != "$$" ] &&
      { tr '\0' '\n' <"$proc/environ"; } 2>/dev/null | grep -q -e '^RUNNER_TRACKING_ID=.' -e '^GITHUB_ACTIONS='; then
      kill -9 "$pid" 2>/dev/null || true
    fi
  done
}

clear_docker() {
  docker info >/dev/null 2>&1 || return 0
  docker ps -aq | xargs -r docker rm -fv >/dev/null || true
  docker network prune -f >/dev/null || true
  docker volume prune -f >/dev/null || true
}

clear_checkouts() {
  for git_dir in "$base"/work/*/*/.git; do
    [ -d "$git_dir" ] || continue
    rm -rf "$git_dir/hooks"
    git --git-dir="$git_dir" config --local --get-regexp '^(http\..*\.extraheader|core\.hookspath|core\.fsmonitor)$' 2>/dev/null |
      while read -r key _; do
        git --git-dir="$git_dir" config --local --unset-all "$key" || true
      done
  done
}

clear_files() {
  find /tmp /var/tmp /dev/shm -mindepth 1 -maxdepth 1 \
    ! -path /tmp/runner ! -path /tmp/multipart-uploads ! -name 'sandbox-api-*' \
    -exec rm -rf {} + 2>/dev/null || true
  home=${HOME:-/root}
  rm -rf "$base/tmp" "$base/work/_temp" "$base/work/_actions" /actions-runner/_diag "$base/actions-runner" \
    "$home/.docker/config.json" "$home/.git-credentials" "$home/.gitconfig" "$home/.netrc" \
    "$home/.npmrc" "$home/.yarnrc.yml" "$home/.pypirc" "$home/.aws" "$home/.azure" \
    "$home/.config/gcloud" "$home/.config/gh" "$home/.kube" "$home/.ssh" \
    "$home/.cargo/credentials" "$home/.cargo/credentials.toml" "$home/.terraform.d/credentials.tfrc.json"
}

tries=0
until docker info >/dev/null 2>&1; do
  tries=$((tries + 1))
  if [ "$tries" -ge 30 ]; then
    echo "Docker is not running, see /var/log/dockerd.log" >&2
    break
  fi
  sleep 1
done

stop_job_processes
clear_docker
clear_checkouts
clear_files
mkdir -p "$base/work" "$base/toolcache" "$base/cache" "$base/tmp"

export RUNNER_ALLOW_RUNASROOT=1
export ACTIONS_RUNNER_HOOK_JOB_STARTED=/usr/local/bin/job-started.sh
export RUNNER_TOOL_CACHE="$base/toolcache" AGENT_TOOLSDIRECTORY="$base/toolcache"
export GOCACHE="$base/cache/go-build" GOMODCACHE="$base/cache/go-mod"
export npm_config_cache="$base/cache/npm" PIP_CACHE_DIR="$base/cache/pip"

cd /actions-runner
if [ ! -L _work ]; then
  rm -rf _work
  ln -s "$base/work" _work
fi
exec ./run.sh --jitconfig "$JIT_CONFIG"
