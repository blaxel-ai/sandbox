#!/usr/bin/env bash
# Populates hub/builder/embedded/ with the binaries the builder image ships.
# They are not committed: kernel.bin is ~36MB.
#
# Both come from metamorph, which embeds the same pair today, so the artefacts a
# sandbox build produces stay interchangeable with a metamorph one.
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
wrapper=$METAMORPH/templates/unikraft_wrapper

if [[ ! -f $kernel ]]; then
  echo "kernel not found: $kernel" >&2
  echo "run metamorph's scripts/download-mk3-kernels.sh first" >&2
  exit 1
fi

cp "$kernel" "$DEST/kernel.bin"
cp "$wrapper" "$DEST/metamorph-wrapper"

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
chmod +x "$DEST/metamorph-wrapper"

ls -lh "$DEST"
