Build output of `sandbox-api/kmod/virtio_ring_resync`, embedded into the binary
by `src/lib/networking/virtio_watchdog.go`:

- `virtio_ring_resync.ko`: the module, built by the `kmod` stage of the
  Dockerfile against the guest kernel in `kmod/guest-kernel.config`.
- `virtio_ring_resync.kernel`: the kernel release and config digest it was
  built for; the watchdog only loads the module into that kernel.

Both are generated; a plain `go build` ships neither and the watchdog then only
reports a broken ring.
