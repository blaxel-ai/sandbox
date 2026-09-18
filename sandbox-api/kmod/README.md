# virtio_ring_resync

Kernel module the virtio watchdog (`src/lib/networking/virtio_watchdog*.go`)
loads into the guest when the kernel logs `virtio_net virtioN: input.0:id X is
not a head!`. See the header of `virtio_ring_resync/virtio_ring_resync.c` for
what it does and why a driver rebind is not an option on Firecracker.

The module reads private `struct vring_virtqueue` fields, so it is built by
the `kmod` stage of the Dockerfile against the exact kernel the mk3 sandboxes
run: upstream `linux-6.12.75` configured with `guest-kernel.config`, which is
the sandboxes' `/proc/config.gz`. At startup the watchdog compares the running
kernel's release and config digest with what the module was built for and only
loads it on a match.

When the guest kernel changes:

```
cd e2e/virtio-watchdog && npm i && BL_ENV=dev REGION=eu-dub-1 IMAGE=blaxel/base-image:latest node probe-kernel.mjs
cp guest-kernel/config ../../sandbox-api/kmod/guest-kernel.config
```

then bump `GUEST_KERNEL` in the Dockerfile if the release moved and check the
struct layouts in `virtio_ring_resync.c` against `drivers/virtio/virtio_ring.c`
of that release. `e2e/virtio-watchdog/resync-kmod.mjs` loads the module into a
live sandbox, breaks the rx queue and checks the module brings it back.

Local build, given a prepared kernel tree (`make olddefconfig modules_prepare`):

```
make -C sandbox-api/kmod/virtio_ring_resync KDIR=~/linux-6.12.75 KBUILD_MODPOST_WARN=1
```

`KBUILD_MODPOST_WARN=1` is needed because `modules_prepare` produces no
`Module.symvers`; the watchdog loads the module with
`MODULE_INIT_IGNORE_MODVERSIONS|MODULE_INIT_IGNORE_VERMAGIC` for the same reason.
