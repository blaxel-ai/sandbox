#!/bin/sh
set -eu

case "${GITHUB_EVENT_NAME:-}" in
pull_request | pull_request_target | pull_request_review | pull_request_review_comment) ;;
*) exit 0 ;;
esac

head=$(jq -r '.pull_request.head.repo.id // "none"' "$GITHUB_EVENT_PATH")
base=$(jq -r '.repository.id // "unknown"' "$GITHUB_EVENT_PATH")
if [ "$head" != "$base" ]; then
  from=$(jq -r '.pull_request.head.repo.full_name // "a deleted repository"' "$GITHUB_EVENT_PATH")
  echo "Blaxel runners do not run pull requests from forks: this one comes from $from." >&2
  exit 1
fi
