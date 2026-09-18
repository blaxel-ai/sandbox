// Load the virtio_ring_resync kernel module into a live sandbox and check the
// network survives it. This exercises the module itself (it loads, the queue
// layout check passes, a forced resync of healthy queues does not break the
// device) independently of sandbox-api, so it works on any image.
//
//   make -C ../../sandbox-api/kmod/virtio_ring_resync KDIR=<linux-6.12.75 tree> KBUILD_MODPOST_WARN=1
//   (cd kmodload && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o kmodload .)
//   KO=../../sandbox-api/kmod/virtio_ring_resync/virtio_ring_resync.ko LOADER=./kmodload/kmodload \
//   IMAGE=blaxel/base-image:latest REGION=eu-dub-1 node resync-kmod.mjs
//
// The loader is needed because busybox insmod cannot ignore vermagic; the
// watchdog in sandbox-api does the same finit_module call.
import { SandboxInstance } from "@blaxel/core";
import { readFileSync } from "node:fs";

const IMAGE = process.env.IMAGE ?? "blaxel/base-image:latest";
const REGION = process.env.REGION;
const NAME = process.env.NAME ?? `resync-kmod-${Date.now().toString(36)}`;
const KO = process.env.KO ?? "../../sandbox-api/kmod/virtio_ring_resync/virtio_ring_resync.ko";
const LOADER = process.env.LOADER;
const KEEP = process.env.KEEP === "1";

if (!LOADER) throw new Error("LOADER=<path to finit_module helper> is required");

const sandbox = await SandboxInstance.createIfNotExists({
  name: NAME,
  image: IMAGE,
  memory: 2048,
  ...(REGION ? { region: REGION } : {}),
});
await sandbox.wait();
console.log(`sandbox ${NAME} ready`);

const run = async (command) => {
  let p;
  for (let attempt = 1; ; attempt++) {
    try {
      p = await sandbox.process.exec({ command, waitForCompletion: true, timeout: 120 });
      break;
    } catch (e) {
      // The edge gateway occasionally 502s while the sandbox is waking up.
      if (attempt === 10 || (e?.status !== 502 && e?.status !== 504)) throw e;
      await new Promise((r) => setTimeout(r, 3000));
    }
  }
  const out = (p.logs ?? "").trim();
  console.log(`$ ${command}\n${out}`);
  return out;
};

let failed = false;
try {
  await sandbox.fs.writeBinary("/tmp/virtio_ring_resync.ko", readFileSync(KO));
  await sandbox.fs.writeBinary("/tmp/kmodload", readFileSync(LOADER));
  await run("chmod +x /tmp/kmodload; uname -r; zcat /proc/config.gz | grep -v '^#' | grep . | sha256sum");

  const config = "ip -4 -o addr show eth0; ip route";
  const before = await run(`${config}; cat /proc/net/dev | grep eth0`);

  // No broken queue: the module must decline (ENOENT) and change nothing.
  const dryRun = await run("/tmp/kmodload /tmp/virtio_ring_resync.ko netdev=eth0");
  if (!/no such file or directory/i.test(dryRun)) {
    failed = true;
    console.error("FAIL: expected ENOENT when no queue is broken");
  }

  // Forced resync of every queue on a healthy device: exercises the whole
  // path (layout check, index/event rewrite, unbreak, interrupt).
  const forced = await run("/tmp/kmodload /tmp/virtio_ring_resync.ko netdev=eth0 force=1");
  if (!/finit_module: <nil>/.test(forced) || !/resynced: [1-9]/.test(forced) || !/delete_module: <nil>/.test(forced)) {
    failed = true;
    console.error("FAIL: forced resync did not load/resync/unload cleanly");
  }
  await run("dmesg | grep -i -E 'resync|virtio_ring_resync|taint' | tail -20");

  const probe = "for i in 1 2 3; do wget -S -O /dev/null -T 5 https://www.google.com/generate_204 2>&1 | grep -q 'HTTP/' && echo ok$i || echo fail$i; done";
  const oks = (out) => (out.match(/\bok\d/g) ?? []).length;

  // The network must still work after the forced resync.
  if (oks(await run(probe)) < 3) {
    failed = true;
    console.error("FAIL: network broken after forced resync");
  }

  // Now the real thing: mark the rx queue broken exactly like BAD_RING() does
  // (the "is not a head!" outcome), check the network dies, and check the
  // module brings it back. Output is collected in one exec, since with a
  // broken rx queue the sandbox cannot be reached from outside anymore.
  const recovery = await run(
    [
      "/tmp/kmodload /tmp/virtio_ring_resync.ko netdev=eth0 queue=input.0 break_queues=1",
      `echo '--- broken ---'; ${probe}`,
      "echo '--- resync ---'; /tmp/kmodload /tmp/virtio_ring_resync.ko netdev=eth0",
      `echo '--- recovered ---'; ${probe}`,
    ].join("; "),
  );
  const section = (name) => recovery.split(`--- ${name} ---`)[1]?.split("--- ")[0] ?? "";
  const brokenPart = section("broken");
  const afterPart = section("recovered");
  if (oks(brokenPart) !== 0) {
    failed = true;
    console.error("FAIL: network kept working with a broken rx queue, the test is not exercising the recovery");
  }
  if (!/finit_module: <nil>[\s\S]*resynced: 1/.test(recovery) || oks(afterPart) < 3) {
    failed = true;
    console.error("FAIL: the resync did not bring the network back");
  }
  await run("dmesg | grep -E 'virtio0' | tail -10");
  const after = await run(`${config}; cat /proc/net/dev | grep eth0`);
  if (before.split("\n").slice(0, -1).join("\n") !== after.split("\n").slice(0, -1).join("\n")) {
    failed = true;
    console.error("FAIL: interface configuration changed");
  }
  await run("lsmod");
} finally {
  if (!KEEP) await SandboxInstance.delete(NAME);
}
console.log(failed ? "RESULT: FAIL" : "RESULT: PASS");
process.exit(failed ? 1 : 0);
