---
name: check-ci
description: Poll CI status on a branch until the "Build and Push Sandbox" workflow completes. Use after pushing to develop or any branch where you need to confirm the build succeeded before proceeding.
---

# Check CI

Poll the **Build and Push Sandbox** workflow on a given branch until it completes, then report the result.

## Usage

When invoked, determine which branch to watch:
- If a branch name was passed as an argument, use that.
- Otherwise, use the branch that was most recently pushed (typically `develop`).

## Steps

### Step 1: Find the latest "Build and Push Sandbox" run

```bash
gh api "repos/blaxel-ai/sandbox/actions/runs?branch=<branch>&per_page=5" \
  --jq '.workflow_runs[] | select(.name == "Build and Push Sandbox") | {id: .id, status: .status, conclusion: .conclusion, url: .html_url, created_at: .created_at}' \
  | head -1
```

If no run is found, tell the user and stop.

### Step 2: Poll until completed

If the run status is not `completed`, set up a recurring check every 30 seconds (rounded to 1 minute for cron) using CronCreate:

- **Cron**: `*/1 * * * *`
- **Prompt**: Re-check the run status using the run ID from Step 1. If `status` is `completed`, report the conclusion and cancel the cron job.

Alternatively, if CronCreate is not available, manually check the status and ask the user to wait.

### Step 3: Report result

Once the run completes, report:
- **Workflow**: Build and Push Sandbox
- **Branch**: the branch name
- **Conclusion**: success / failure / cancelled
- **URL**: link to the run

If the conclusion is `failure`, suggest the user check the logs at the run URL.
