// Exercises the sandbox-api virtio_net watchdog against a live sandbox.
//
//   npm i && node test.mjs                # inject the kernel line, check the rebind kept the network
//   MODE=stress CYCLES=200 node test.mjs  # standby/resume loop trying to hit the real UKP 0.6 race
//
// Env: BL_API_KEY / BL_WORKSPACE / BL_ENV (SDK auth), IMAGE, REGION, MEMORY,
// NAME, WATCHDOG_DISABLED=1 (set BL_DISABLE_VIRTIO_WATCHDOG in the sandbox to
// check the opt-out), IDLE_MS (stress: idle time between requests), KEEP=1.

import { SandboxInstance } from "@blaxel/core";

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
  return { iface, addrs, routes, mtu, up, ifindex };
}

async function inject(sandbox) {
  const before = await snapshotNet(sandbox);
  console.log("before:", before);
  if (!before.iface) return fail("virtio0 has no net interface: not a virtio-net guest?");

  const dmesgBefore = (await sh(sandbox, "dmesg | wc -l")).out;
  const w = await sh(sandbox, `printf '<3>${KMSG_LINE}\\n' > /dev/kmsg`);
  if (w.code !== 0) return fail(`cannot write /dev/kmsg: ${w.out}`);

  // The watchdog unbinds/rebinds the driver; the sandbox is unreachable for
  // ~1s while it does. A fresh exec afterwards proves the network came back.
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
  if (!after) return fail("sandbox unreachable after the rebind");
  console.log("after: ", after);

  const kernel = (await sh(sandbox, `dmesg | tail -n +$((${dmesgBefore} + 1))`)).out;
  console.log("kernel log since injection:\n" + kernel);

  // A rebind creates a new netdev: the ifindex changes even if the name is put back.
  const rebound = before.ifindex !== after.ifindex;
  if (env.WATCHDOG_DISABLED) {
    if (rebound) fail("BL_DISABLE_VIRTIO_WATCHDOG set but the driver was rebound");
    else console.log("OK: opt-out respected, no rebind");
    return;
  }
  if (!rebound) fail("ifindex unchanged after the injected line: watchdog did not react (is sandbox-api from this PR running, and is /dev/kmsg readable?)");
  for (const k of ["iface", "addrs", "routes", "mtu"]) {
    if (before[k] !== after[k]) fail(`${k} changed across rebind:\n  before: ${before[k]}\n  after:  ${after[k]}`);
  }
  if (after.up !== "up" && after.up !== "unknown") fail(`interface not up after rebind: ${after.up}`);
  const egress = await sh(sandbox, "wget -qO- --timeout=5 https://api.blaxel.ai/health >/dev/null 2>&1 || curl -sf --max-time 5 https://api.blaxel.ai/health >/dev/null; echo $?");
  console.log("egress after rebind exit code:", egress.out);
  if (!process.exitCode) console.log("OK: watchdog rebound virtio0 and the network configuration survived");
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
  else await inject(sandbox);
  if (sandbox.errors?.length) console.log("infrastructure errors:", sandbox.errors);
} finally {
  if (env.KEEP) console.log(`KEEP set, leaving ${NAME}`);
  else await SandboxInstance.delete(NAME);
}
