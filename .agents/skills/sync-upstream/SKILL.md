---
name: sync-upstream
description: Port the commits landed on the upstream repo blaxel-ai/sandbox since the last sync into this fork, adapting each one to the fork's renames and removals instead of merging. Use when asked to sync, update or catch up with upstream, blaxel-ai/sandbox, or "the source repo".
---

# Sync upstream

This repo is a fork of [blaxel-ai/sandbox](https://github.com/blaxel-ai/sandbox) with too many
breaking changes for `git merge`. Upstream commits are ported one by one and translated to the
fork's conventions.

`.upstream-sha` at the repo root holds the last upstream `main` commit already handled (ported
or deliberately skipped). It is the only sync state: git's merge-base never moves because we
never merge.

## Step 1: List what is new

```bash
git fetch origin main
git remote get-url upstream >/dev/null 2>&1 || git remote add upstream https://github.com/blaxel-ai/sandbox.git
# upstream also has a tag named main: fetch the branch explicitly
git fetch upstream +refs/heads/main:refs/remotes/upstream/main
git log --reverse --no-merges --format='%H %s' "$(cat .upstream-sha)"..upstream/main
```

Empty list: report "already up to date" with the upstream SHA and stop.

Create the branch from `origin/main`: `git checkout -b sync/upstream-$(date +%Y-%m-%d) origin/main`.

## Step 2: Triage each commit

Read every commit (`git show <sha>`) before touching anything, and put it in one bucket:

- **Port**: behavior change in code the fork still has (`sandbox-api/`, `hub/`, `docker/`, `e2e/`, scripts).
- **Skip**: touches only things the fork removed or replaced (list below), upstream-only release
  bumps, or Blaxel-only infra. Note why.
- **Ask**: changes a runtime contract or a CI/publish flow in a way that conflicts with a fork
  decision. Stop and ask the user before porting it.

## Step 3: Port, in upstream order

```bash
git cherry-pick -x <sha>
```

Then, conflict or not, translate the result with the rules below. A clean cherry-pick can still
reintroduce an old name (new file, new import), so always run the leftover check after each one.
Resolve conflicts by keeping the fork's side for names and the upstream side for logic.
Use `git cherry-pick --skip` only for commits triaged as Skip after they turn out empty.

Keep one commit per upstream commit: the `-x` trailer is the audit trail. Amend the translation
into that same commit (`git commit --amend --no-edit`).

### Fork translation rules

| Upstream | Fork |
|---|---|
| Go module `github.com/blaxel-ai/sandbox-api` | `github.com/basetenlabs/sandbox/sandbox-api` (also in `integration-tests/go.mod`, ldflags, Dockerfile) |
| package `src/lib/blaxel` | `src/lib/scale` |
| default scale-to-zero control file | lives under `/bl` |
| `/blaxel/app` (web framework hub images) | `/app` |
| `/blaxel` workdir (generic and agent hub images) | `/workspace`, default `HOME` |
| `*.bl.run`, `run.blaxel.ai` (prod hosts) | `*.b10.co` |
| `*.runv2.blaxel.dev`, `run.blaxel.dev` (dev hosts) | `*.dev.b10.co` |
| env vars `BL_<NAME>` (code, Dockerfiles, scripts, CI, docs) | `BASETEN_SANDBOX_<NAME>`, plain prefix swap (`BL_ENV` → `BASETEN_SANDBOX_ENV`) |
| `docs.blaxel.ai/...` | `docs.baseten.co/sandboxes/...` (closest page) |
| image `ghcr.io/blaxel-ai/sandbox` | `docker.io/baseten/sandbox` |
| "Blaxel" in texts, template.json, skills, README | "Baseten" or a neutral name |
| hub publish job, `BL_*` CI vars/secrets | `publish-hub`, `HUB_*` vars/secrets |
| CI on `develop` | CI on `main` only |
| arm64 sandbox-api image | not built |

**Keep as is** (runtime contracts with the platform): `X-Blaxel-*` headers, identity token path, archive format and paths.

**Removed in the fork, skip upstream changes to them**: `.bruno/`, CodSpeed workflow and
Makefile target, `blaxel.toml` files, Makefile targets calling the `bl` CLI, `hub/node-test.py`,
skills `playwright-e2e`, `android-e2e`, `run-e2e`, the Blaxel logo. If an upstream commit adds a
new Blaxel-CLI or Blaxel-SDK based tool, skip it too and mention it in the PR.

Found a new divergence while porting? Add it to the table in this skill in the same PR.

### Leftover check (after each commit)

```bash
git show HEAD | perl -ne 'next unless /^\+/; s/X-Blaxel-[\w-]+//gi; print "$.: $_" if /blaxel|bl\.run|\bBL_/i'
```

Every hit must be fixed or be a runtime contract from the "Keep as is" list.

## Step 4: Verify

Same checks as CI (`.github/workflows/test.yaml`):

```bash
cd sandbox-api
go mod tidy && (cd integration-tests && go mod tidy) && gofmt -w . && git diff --exit-code
go build ./... && go test ./...
```

If a ported commit touches a feature covered by integration tests, run the relevant ones with
the `run-integration-tests` skill. If a hub image changed, build it locally (`add-hub-template`
skill describes the layout).

## Step 5: Record and ship

Write the last upstream SHA you handled (ported or skipped, never one you stopped before) into
`.upstream-sha`, commit it as `chore: sync upstream to <short sha>`.

Open the PR against `main`. Body: one line per upstream commit, in order, with its link
(`https://github.com/blaxel-ai/sandbox/commit/<sha>`) and its bucket: ported, adapted (say what
was translated), or skipped (say why). Put anything triaged as Ask at the top.
