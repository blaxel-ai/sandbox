---
name: deploy-dev
description: Deploy the app to the dev environment. Use after a PR is marked as reviewable (ready for review) so reviewers can check out the changes running in dev.
---

# Deployment Phase

After the self-review passes and the PR is confirmed ready for human review, proceed to merge into `develop` and trigger the dev deployment.

## Step 1: Merge into develop

Using git commands directly:

1. Ensure the working tree is clean (`git status --porcelain` must be empty)
2. Save the current branch name: `git branch --show-current`
3. Fetch latest from origin: `git fetch origin`
4. Checkout develop: `git checkout develop`
5. Pull latest develop: `git pull origin develop`
6. Merge the feature branch into develop: `git merge <feature-branch> --no-edit`
7. Push develop: `git push origin develop`
8. Switch back to the feature branch: `git checkout <feature-branch>`

> **If the merge fails** with conflicts, do NOT abort. Instead, resolve conflicts interactively: for each conflicting file, show the user both sides of the conflict and ask which version to keep (or how to combine them). Once the user has provided input for every conflict, stage the resolved files, complete the merge commit, and continue with the deployment. Only abort if the user explicitly asks to cancel.

## Step 2: Confirm deployment

Send a brief message confirming that the merge into `develop` succeeded and both the controlplane and infrastructure runs completed successfully, including the run URLs.
