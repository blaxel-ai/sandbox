---
name: check-deploy
description: Watch CI until the "Build and Push Sandbox" workflow completes. For dev deployments, watches the develop branch. For prod deployments, watches the latest tag. Use after merging into develop or pushing a release tag.
---

# Check Deploy

Watch the **Build and Push Sandbox** workflow until it completes, then report the result.

## Determine what to watch

- **Dev deployment** (default, or when the context is a merge into `develop`): watch the `develop` branch.
- **Prod deployment** (when the context is a release or tag push): watch the latest tag. Find it with:
  ```bash
  git describe --tags --abbrev=0
  ```
  Then query workflow runs filtered by that tag.

If explicitly told which branch or tag to watch, use that instead.

## Steps

### Step 1: Find the latest "Build and Push Sandbox" run

For **dev** (branch):
```bash
gh api "repos/blaxel-ai/sandbox/actions/runs?branch=develop&per_page=5" \
  --jq '.workflow_runs[] | select(.name == "Build and Push Sandbox") | {id: .id, status: .status, conclusion: .conclusion, url: .html_url, created_at: .created_at}' \
  | head -1
```

For **prod** (tag):
```bash
gh api "repos/blaxel-ai/sandbox/actions/runs?event=push&per_page=10" \
  --jq '.workflow_runs[] | select(.name == "Build and Push Sandbox" and .head_branch == "<tag>") | {id: .id, status: .status, conclusion: .conclusion, url: .html_url, created_at: .created_at}' \
  | head -1
```

If no run is found, tell the user and stop.

### Step 2: Watch the run

Use `gh run watch` to follow the run until completion:
```bash
gh run watch <run_id> --exit-status
```

This blocks until the run finishes and returns a non-zero exit code on failure.

**The deployment is considered done as soon as any `build-s3-hub` job completes successfully.** You do not need to wait for the entire workflow to finish. If you see `build-s3-hub` jobs pass in the output, you can report success early.

### Step 3: Report result

Once deployed, report:
- **Workflow**: Build and Push Sandbox
- **Target**: branch or tag name
- **Conclusion**: success / failure / cancelled
- **URL**: link to the run

If the conclusion is `failure`, suggest the user check the logs at the run URL.
