// Exercises the sandbox-api virtio_net watchdog against a live sandbox.
//
//   npm i && node test.mjs                # inject the kernel line, check the watchdog reacts and the network is intact
//   MODE=break KO=... LOADER=... node test.mjs
//                                         # really break the rx queue, inject the line, check the watchdog recovers it
//   MODE=stress CYCLES=200 node test.mjs  # standby/resume loop trying to hit the real UKP 0.6 race
//
// Env: BL_API_KEY / BL_WORKSPACE / BL_ENV (SDK auth), IMAGE (built from this
// branch), REGION, MEMORY, NAME, WATCHDOG_DISABLED=1 (set
// BL_DISABLE_VIRTIO_WATCHDOG in the sandbox to check the opt-out), IDLE_MS
// (stress: idle time between requests), KO + LOADER (break: the
// virtio_ring_resync.ko and finit_module helper, see resync-kmod.mjs), KEEP=1.

import { SandboxInstance } from "@blaxel/core";
import { readFileSync } from "node:fs";

const env = process.env;
const MODE = env.MODE ?? "inject";
const NAME = env.NAME ?? `virtio-watchdog-${Date.now().toString(36)}`;
const KMSG_LINE = "virtio_net virtio0: input.0:id 171 is not a head!";

async function sh(sandbox, command, timeout = 30) {
  const p = await sandbox.process.exec({ command: `sh -c ${JSON.stringify(command)}`, waitForCompletion: true, timeout });
  return { code: p.exitCode, out: (p.logs ?? "").trim() };
}

function fail(msg) {
  console.error(`FAIL: ${msg}`);
  process.exitCode = 1;
}

async function snapshotNet(sandbox) {
  const iface = (await sh(sandbox, "ls /sys/bus/virtio/devices/virtio0/net")).out;
  const addrs = (await sh(sandbox, `ip -o -4 addr show dev ${iface} | awk '{print $4}' | sort`)).out;
  const routes = (await sh(sandbox, `ip -o route show dev ${iface} | sort`)).out;
  const mtu = (await sh(sandbox, `cat /sys/class/net/${iface}/mtu`)).out;
  const up = (await sh(sandbox, `cat /sys/class/net/${iface}/operstate`)).out;
  const ifindex = (await sh(sandbox, `cat /sys/class/net/${iface}/ifindex`)).out;
  const tainted = (await sh(sandbox, "cat /proc/sys/kernel/tainted")).out;
  return { iface, addrs, routes, mtu, up, ifindex, tainted };
}

async function inject(sandbox, breakQueue) {
  const before = await snapshotNet(sandbox);
  console.log("before:", before);
  if (!before.iface) return fail("virtio0 has no net interface: not a virtio-net guest?");

  const dmesgBefore = (await sh(sandbox, "dmesg | wc -l")).out;
  let trigger = `printf '<3>${KMSG_LINE}\\n' > /dev/kmsg`;
  if (breakQueue) {
    // Mark the rx queue broken the way BAD_RING() does, so the injected line
    // is followed by the real symptom: no packet gets in until the watchdog
    // resyncs the queue.
    await sandbox.fs.writeBinary("/tmp/virtio_ring_resync.ko", readFileSync(env.KO));
    await sandbox.fs.writeBinary("/tmp/kmodload", readFileSync(env.LOADER));
    trigger = `chmod +x /tmp/kmodload && /tmp/kmodload /tmp/virtio_ring_resync.ko netdev=${before.iface} queue=input.0 break_queues=1 && ${trigger}`;
  }
  const w = await sh(sandbox, trigger);
  if (w.code !== 0) return fail(`trigger failed: ${w.out}`);

  // Give the watchdog time to load the module and, in break mode, the network
  // time to come back.
  await new Promise((r) => setTimeout(r, 4000));
  let after;
  for (let i = 0; i < 5; i++) {
    try {
      after = await snapshotNet(sandbox);
      break;
    } catch (e) {
      console.log(`sandbox not reachable yet (${e.message}), retrying`);
      await new Promise((r) => setTimeout(r, 2000));
    }
  }
  if (!after) return fail("sandbox unreachable after the injected line (break mode: the watchdog did not recover the rx queue)");
  console.log("after: ", after);

  const kernel = (await sh(sandbox, `dmesg | tail -n +$((${dmesgBefore} + 1))`)).out;
  console.log("kernel log since injection:\n" + kernel);

  // The watchdog loads the (unsigned, out-of-tree) resync module, which
  // taints the kernel the first time; its resync line in dmesg is the proof it
  // ran and found a broken queue.
  // In break mode the trigger itself loads the module, so only the resync
  // line can be attributed to the watchdog.
  const resynced = /: resync \(broken=1/.test(kernel);
  const reacted = breakQueue ? resynced : /virtio_ring_resync/.test(kernel) || before.tainted !== after.tainted;
  if (env.WATCHDOG_DISABLED) {
    if (reacted) fail("BL_DISABLE_VIRTIO_WATCHDOG set but the watchdog loaded the resync module");
    else console.log("OK: opt-out respected, watchdog did not react");
    return;
  }
  if (!reacted) fail("no resync module activity after the injected line: watchdog did not react (is sandbox-api from this PR running, with the module built in, and is /dev/kmsg readable?)");
  if (breakQueue && !resynced) fail("the rx queue was broken but the watchdog did not resync it");
  for (const k of ["iface", "addrs", "routes", "mtu", "ifindex"]) {
    if (before[k] !== after[k]) fail(`${k} changed across the recovery:\n  before: ${before[k]}\n  after:  ${after[k]}`);
  }
  if (after.up !== "up" && after.up !== "unknown") fail(`interface not up after the recovery: ${after.up}`);
  const egress = await sh(sandbox, "wget -S -O /dev/null -T 5 https://www.google.com/generate_204 2>&1 | grep -q 'HTTP/'; echo $?");
  console.log("egress after the recovery exit code:", egress.out);
  if (egress.out !== "0") fail("no egress after the recovery");
  if (!process.exitCode) console.log(`OK: watchdog reacted${breakQueue ? ", resynced the broken rx queue" : ""} and the network configuration survived`);
}

async function stress(sandbox) {
  const cycles = Number(env.CYCLES ?? 100);
  const idle = Number(env.IDLE_MS ?? 5000);
  console.log(`${cycles} standby/resume cycles, ${idle}ms idle each; watching dmesg for "is not a head"`);
  for (let i = 1; i <= cycles; i++) {
    await new Promise((r) => setTimeout(r, idle));
    // Several requests back-to-back so packets are in flight right after resume.
    const results = await Promise.allSettled([sh(sandbox, "true"), sh(sandbox, "true"), sh(sandbox, "true")]);
    const failed = results.filter((r) => r.status === "rejected").length;
    let hit = "";
    try {
      hit = (await sh(sandbox, "dmesg | grep -c 'is not a head' || true")).out;
    } catch (e) {
      console.log(`cycle ${i}: sandbox unreachable (${e.message})`);
      continue;
    }
    if (failed || hit !== "0") console.log(`cycle ${i}: ${failed} failed requests, ${hit} ring errors in dmesg`);
    if (hit !== "0") {
      console.log("race reproduced. kernel log:\n" + (await sh(sandbox, "dmesg | grep -A5 'is not a head'")).out);
      console.log(await snapshotNet(sandbox));
      return;
    }
  }
  console.log(`no ring error after ${cycles} cycles`);
}

const envs = [];
if (env.WATCHDOG_DISABLED) envs.push({ name: "BL_DISABLE_VIRTIO_WATCHDOG", value: "true" });

console.log(`creating sandbox ${NAME}`);
const sandbox = await SandboxInstance.createIfNotExists({
  name: NAME,
  image: env.IMAGE ?? "blaxel/base-image:latest",
  memory: Number(env.MEMORY ?? 2048),
  region: env.REGION,
  ttl: env.TTL ?? "1h",
  envs,
});
await sandbox.wait();
try {
  if (MODE === "stress") await stress(sandbox);
  else if (MODE === "break") await inject(sandbox, true);
  else await inject(sandbox, false);
  if (sandbox.errors?.length) console.log("infrastructure errors:", sandbox.errors);
} finally {
  if (env.KEEP) console.log(`KEEP set, leaving ${NAME}`);
  else await SandboxInstance.delete(NAME);
}
