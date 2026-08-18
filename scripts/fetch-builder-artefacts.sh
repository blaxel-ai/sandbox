#!/usr/bin/env bash
# Populates hub/builder/embedded/ with the binaries the builder image ships.
# They are not committed: kernel.bin is ~36MB.
#
# The kernel comes from metamorph so the artefacts a sandbox build produces stay
# interchangeable with a metamorph one.
#
# The unikraft wrapper does NOT come from here any more. This script used to copy
# metamorph's templates/unikraft_wrapper, which looks like the wrapper and is not:
# metamorph's Dockerfile overwrites that path with a published image
# (COPY --from=unikraft-wrapper), so the committed file is a stale artefact — six
# months older than its own .c — that nothing in metamorph reads. Shipping it gave
# images that ran on mk3.1 and never answered on mk3.0, which delegates the user
# switch to the wrapper. hub/builder/Dockerfile now pulls the same image metamorph
# does.
set -euo pipefail

DEST=${DEST:-hub/builder/embedded}
METAMORPH=${METAMORPH:-../metamorph}

mkdir -p "$DEST"

if [[ ! -d $METAMORPH ]]; then
  echo "metamorph checkout not found at $METAMORPH" >&2
  echo "set METAMORPH=/path/to/metamorph" >&2
  exit 1
fi

kernel=$METAMORPH/templates/mk3-kernels/base-compat/kernel.bin

if [[ ! -f $kernel ]]; then
  echo "kernel not found: $kernel" >&2
  echo "run metamorph's scripts/download-mk3-kernels.sh first" >&2
  exit 1
fi

cp "$kernel" "$DEST/kernel.bin"

# The Dockerfile templates for projects that ship no recipe of their own. Taken
# from metamorph rather than rewritten: a project builds the same whichever
# builder handles it, and a template that drifts silently produces an image that
# differs from what the customer had yesterday.
for tmpl in node python golang; do
  src=$METAMORPH/templates/dockerfile.$tmpl.tmpl
  if [[ ! -f $src ]]; then
    echo "missing $src" >&2
    exit 1
  fi
  cp "$src" "$DEST/dockerfile.$tmpl.tmpl"
done

ls -lh "$DEST"
