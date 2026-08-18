# Image Builder (internal)

Builds a customer source context into the `kraft/` artefact set the compute
plane boots from, entirely inside a sandbox. Replaces the external build service
(ENG-4721).

Not a user-facing template: `template.json` sets `hidden: true`.

## What runs where

| | |
|---|---|
| `buildkitd` | started by `entrypoint.sh`, state on `/scratch` |
| `blbuild` | the pipeline (`/blbuild`, source in `blbuild/`), one invocation per build |
| `/opt/blaxel/kernel.bin` | shipped as `kraft/bin/kernel` |
| `/opt/blaxel/metamorph-wrapper` | injected at `/bin/metamorph-wrapper` |
| `/usr/local/bin/blfs` | injected at `/usr/local/bin/blfs`, taken from this image at build time |

## The embedded artefacts

`hub/builder/embedded/` is **not committed**: this repository is public and the
mk3 kernel is a third-party binary from `index.unikraft.io`, so shipping it here
would redistribute it under our MIT licence. Both artefacts live in the internal
`metamorph` repository, which is where they are already maintained.

- **In CI**: `.github/workflows/build.yaml` sparse-checkouts them from
  `blaxel-ai/metamorph` using the `METAMORPH_ARTEFACTS_TOKEN` secret, for this
  image only. Without that secret (a pull request from a fork) `builder` is
  dropped from the build matrix instead of failing the run.
- **Locally**:

  ```sh
  scripts/fetch-builder-artefacts.sh          # expects ../metamorph
  METAMORPH=/path/to/metamorph scripts/fetch-builder-artefacts.sh
  docker build -f hub/builder/Dockerfile .    # context is the repo root
  ```

`bl push` does not work for this image: it uses its target directory as the build
context, and the Dockerfile reads from the repository root like every other hub
image.

## Non-negotiable details

Each of these came from a build that failed in a confusing way:

- **erofs-utils >= 1.9.** 1.8.2 produces an image `fsck.erofs` reports as
  corrupted ("no enough room for the root inode").
- **The ephemeral volume is optional.** It is mk3.1-only, and on mk3.0 the API
  accepts the attachment and silently ignores it, so the entrypoint falls back to
  a tmpfs sized at 90% of RAM rather than refusing to start.
- **`buildctl` is a separate apk package** from `buildkit`.
- **`--oci-worker-snapshotter=overlayfs`**: it used to be `native`, on the
  reasoning that overlayfs cannot stack on the sandbox rootfs. That holds for `/`
  (a read-only EROFS under a RAM overlay) but buildkit writes to `/scratch`, a
  tmpfs or a volume, where stacking works. native copies the accumulated snapshot
  once per layer, so a build cost the sum of prefixes of its base image: measured
  at 9 992MB of scratch for `hub/nextjs` against 5 108MB on overlayfs, and
  `hub/cua-xfce` (27-layer base) could not build at all under native, at 14GB or
  28GB, while overlayfs built it in 114s.
