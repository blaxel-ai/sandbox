#!/bin/sh
set -eu

case "${GITHUB_EVENT_NAME:-}" in
pull_request | pull_request_target | pull_request_review | pull_request_review_comment)
  head_repo=.pull_request.head.repo
  ;;
workflow_run)
  head_repo=.workflow_run.head_repository
  ;;
*) exit 0 ;;
esac

head=$(jq -r "$head_repo.id // \"none\"" "$GITHUB_EVENT_PATH")
base=$(jq -r '.repository.id // "unknown"' "$GITHUB_EVENT_PATH")
if [ "$head" != "$base" ]; then
  from=$(jq -r "$head_repo.full_name // \"a deleted repository\"" "$GITHUB_EVENT_PATH")
  echo "Blaxel runners do not run jobs from forks: this one comes from $from." >&2
  exit 1
fi
