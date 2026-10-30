# Agent notes

See [CONTRIBUTING.md](CONTRIBUTING.md) for how the repo is organized and built.

## Deploying to dev

- Add the `deploy-to-dev` label to the PR. A workflow merges the branch into `develop`, runs the develop builds, and comments the result on the PR.
- It swaps the label for `on-develop` on success, or `develop-ci-failed` on failure. On a merge conflict it only comments: resolve it and add the label again.
- You need write access to the repo. Only the labeled commit is deployed: after a new push, add the label again.
- A PR that changes `.github/workflows/` cannot be deployed this way. Merge it into `develop` by hand.
- Never push directly to `main`.
