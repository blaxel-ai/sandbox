---
name: test-changes
description: Required test coverage for any sandbox-api change — which tests to add, where, how to run them locally, and how CI runs them automatically. Use whenever modifying Go code under sandbox-api/.
---

# Test A Change To sandbox-api

Every behaviour change to `sandbox-api/` ships with tests. Two layers, both
mandatory when applicable, both run by CI on the PR
(`.github/workflows/test.yaml` — nothing here has to be triggered by hand):

| Layer | Location | Runs against | CI job |
|-------|----------|--------------|--------|
| Unit | `sandbox-api/src/**/<pkg>_test.go` | the package, no server | `unit` |
| Integration | `sandbox-api/integration-tests/tests/<area>/` | a live API over HTTP | `integration` (matrix: `root`, `workload-user`) |

## Checklist before opening the PR

- [ ] Unit tests next to the code for every new function whose behaviour is not
      obvious from its signature, including the failure paths (invalid input,
      missing user, denied permission).
- [ ] An integration test in the matching `tests/<area>/` directory for every
      endpoint or endpoint behaviour you added or changed.
- [ ] If the change is about **what the API runs as**, add it to
      `tests/identity/` and make sure it is meaningful in both CI modes.
- [ ] Prove the test can fail: run it against a build without your change (or
      temporarily revert the behaviour). A test that passes either way is not a
      test.
- [ ] `cd sandbox-api && go vet ./... && go test ./...` clean, and `gofmt -l`
      lists none of the files you touched (the tree is not fully gofmt'd, so CI
      checks vet and tests only).

## Running locally

```bash
# Unit
cd sandbox-api && go test ./...

# Integration, against the dev container (see the local-env skill)
docker-compose up dev
make integration-test

# Integration, against a binary you just built (what CI does)
cd sandbox-api && go build -o /tmp/sandbox-api . && sudo /tmp/sandbox-api -port 8080 &
cd integration-tests && API_BASE_URL=http://localhost:8080 go test -v ./tests/<area>/...
```

`tests/network` needs a real sandbox network stack and fails on a bare host, so
CI does not run it; run it inside a deployed sandbox (see the run-e2e skill).

## The two identity modes

`sandbox-api` can supervise an unprivileged workload user: it keeps root for
drive mounts, the egress tunnel and keep-alive, and runs everything it does *for
the user* (processes, terminals, filesystem endpoints) as the Dockerfile `USER`.
Both modes are contracts, so CI runs the suite twice:

```bash
# root mode: no identity configured, everything runs as the API user
sudo /tmp/sandbox-api -port 8080

# workload-user mode: API root, workload scoped to an unprivileged user
sudo ./integration-tests/run_identity_tests.sh            # creates the user, boots the API, runs tests/identity
```

`tests/identity` keys off the `WORKLOAD_USER` environment variable: set it to
the identity the API was started with and the scoping assertions run; leave it
unset and the "an API with no identity keeps running as root" assertions run
instead. Write new identity tests the same way so they stay meaningful in both
CI jobs.

If your change touches process spawning, terminals, filesystem endpoints or
drive mounts, ask what happens in *both* modes before assuming it is covered.
