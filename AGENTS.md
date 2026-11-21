# Agent notes

See [CONTRIBUTING.md](CONTRIBUTING.md) for how the repo is organized and built.

## Deploying to dev

- Add the `deploy-to-dev` label to the PR. A workflow merges the branch into `develop`, runs the develop builds, and comments the result on the PR.
- It swaps the label for `on-develop` on success, or `develop-ci-failed` when a build fails. If the merge conflicts or the push to `develop` fails, it removes the label and only comments: fix it and add the label again.
- You need write access to the repo. Only the labeled commit is deployed: after a new push, add the label again.
- A PR that changes `.github/workflows/` cannot be deployed this way. Merge it into `develop` by hand.
- Never push directly to `main`.

## Errors

- Every error a handler answers comes from the registry in `sandbox-api/src/lib/apierrors`: `h.SendError(c, apierrors.X, err)`, or `WriteError` for a fixed message. No hand-built `ErrorResponse` or `gin.H{"error": ...}`; `catalog_test.go` fails on them and on any site `catalog.csv` does not list.
- A new code gets its entry in `registry.go` (dispatch state and retry fields read from the emitting code), its rows in `catalog.csv`, and `UPDATE_EXPORT=1 go test ./src/lib/apierrors/` to refresh `registry.json`. Never change a shipped code or status.
