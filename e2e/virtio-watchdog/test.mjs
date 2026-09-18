// Exercises the sandbox-api virtio_net watchdog against a live sandbox.
//
//   npm i && node test.mjs                # inject the kernel line, check the watchdog reacts and the network is intact
//   MODE=break KO=... LOADER=... node test.mjs
//                                         # really break the rx queue, inject the line, check the watchdog recovers it
//   MODE=stress CYCLES=200 node test.mjs  # queries -> keepAlive -> standby loop trying to hit the real UKP 0.6 race
//
// Env: BL_API_KEY / BL_WORKSPACE / BL_ENV (SDK auth), IMAGE (built from this
// branch), REGION, MEMORY, NAME, WATCHDOG_DISABLED=1 (set
// BL_DISABLE_VIRTIO_WATCHDOG in the sandbox to check the opt-out), stress:
// BURST (queries per cycle), KEEPALIVE_S, IDLE_MS (time left for standby
// after the keepAlive ends), CONTINUE=1 (keep cycling after a hit), KO + LOADER (break: the
// virtio_ring_resync.ko and finit_module helper, see resync-kmod.mjs), KEEP=1.

import { SandboxInstance } from "@blaxel/core";
import { readFileSync } from "node:fs";

const env = process.env;
const MODE = env.MODE ?? "inject";
const NAME = env.NAME ?? `virtio-watchdog-${Date.now().toString(36)}`;
const KMSG_LINE = "virtio_net virtio0: input.0:id 171 is not a head!";

// the edge gateway answers 502/504 while a sandbox is still coming up or resuming
async function retryGateway(fn, attempts = 10) {
  for (let i = 1; ; i++) {
    try {
      return await fn();
    } catch (e) {
      if (i === attempts || !/\b50[24]\b/.test(`${e.status ?? ""} ${e.message}`)) throw e;
      await new Promise((r) => setTimeout(r, 2000));
    }
  }
}

async function sh(sandbox, command, timeout = 30) {
  const p = await retryGateway(() =>
    sandbox.process.exec({ command: `sh -c ${JSON.stringify(command)}`, waitForCompletion: true, timeout }),
  );
  return { code: p.exitCode, out: (p.logs ?? "").trim() };
}

// A keepAlive process pins the sandbox out of standby for the whole run, so a
// 502/504 during the test can only mean the network really is down. Not used
// in stress mode, whose whole point is the standby/resume cycle.
async function keepAlive(sandbox) {
  const p = await retryGateway(() =>
    sandbox.process.exec({ name: "keepalive", command: "sleep infinity", keepAlive: true, timeout: 0 }),
  );
  return () => sandbox.process.kill(p.pid).catch(() => {});
}

function fail(msg) {
  console.error(`FAIL: ${msg}`);
  process.exitCode = 1;
}

async function snapshotNet(sandbox) {
  const iface = (await sh(sandbox, "ls /sys/bus/virtio/devices/virtio0/net")).out;
  // /proc fallbacks: slim images ship without iproute2
  const addrs = (
    await sh(
      sandbox,
      `if command -v ip >/dev/null; then ip -o -4 addr show dev ${iface} | awk '{print $4}'; else grep -B1 '/32 host' /proc/net/fib_trie | grep -oE '[0-9]+(\\.[0-9]+){3}'; fi | sort -u`,
    )
  ).out;
  const routes = (
    await sh(sandbox, `if command -v ip >/dev/null; then ip -o route show dev ${iface}; else grep "^${iface}" /proc/net/route | cut -f1-3,8; fi | sort`)
  ).out;
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
    await retryGateway(() => sandbox.fs.writeBinary("/tmp/virtio_ring_resync.ko", readFileSync(env.KO)));
    await retryGateway(() => sandbox.fs.writeBinary("/tmp/kmodload", readFileSync(env.LOADER)));
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

// The pattern seen in front of the real ring desync: a burst of requests, a
// short keepAlive process, then no traffic until the sandbox goes to standby,
// so the next burst lands on a resume.
async function stress(sandbox) {
  const cycles = Number(env.CYCLES ?? 100);
  const keepAliveSec = Number(env.KEEPALIVE_S ?? 5);
  const idle = Number(env.IDLE_MS ?? 20000);
  const burst = Number(env.BURST ?? 5);
  console.log(
    `${cycles} cycles of: ${burst} queries -> ${keepAliveSec}s keepAlive -> ${idle}ms idle (standby) ; watching dmesg for "is not a head"`,
  );
  let recovered = 0;
  for (let i = 1; i <= cycles; i++) {
    const t0 = Date.now();
    const results = await Promise.allSettled(
      Array.from({ length: burst }, (_, n) => sh(sandbox, `echo ${n}; cat /proc/net/dev | grep eth0`)),
    );
    const failed = results.filter((r) => r.status === "rejected").length;
    let hit = "";
    try {
      await retryGateway(() =>
        sandbox.process.exec({ command: `sleep ${keepAliveSec}`, keepAlive: true, timeout: keepAliveSec + 5, waitForCompletion: true }),
      );
      hit = (await sh(sandbox, "dmesg | grep -c 'is not a head' || true")).out;
    } catch (e) {
      console.log(`cycle ${i}: sandbox unreachable (${e.message})`);
      continue;
    }
    if (failed || hit !== "0") console.log(`cycle ${i}: ${failed} failed requests, ${hit} ring errors in dmesg (${Date.now() - t0}ms)`);
    if (hit !== "0") {
      const kernel = (await sh(sandbox, "dmesg | grep -E -A3 'is not a head'")).out;
      const resyncs = (kernel.match(/: resync \(broken=1/g) ?? []).length;
      console.log(`race hit. kernel log:\n${kernel}`);
      console.log(await snapshotNet(sandbox));
      const egress = await sh(sandbox, "curl -sS -m 10 -o /dev/null https://www.google.com/generate_204; echo $?");
      console.log(`egress after the hit exit code: ${egress.out}`);
      if (resyncs < Number(hit)) return fail(`${hit} ring errors but only ${resyncs} resyncs: the watchdog missed one`);
      if (egress.out !== "0") return fail("no egress after the recovery");
      recovered = Number(hit);
      if (!env.CONTINUE) return console.log(`OK: race reproduced and recovered ${recovered} time(s)`);
    }
    await new Promise((r) => setTimeout(r, idle));
  }
  console.log(recovered ? `OK: ${recovered} ring error(s) over ${cycles} cycles, all recovered` : `no ring error after ${cycles} cycles`);
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
let stopKeepAlive = async () => {};
try {
  if (MODE === "stress") await stress(sandbox);
  else {
    stopKeepAlive = await keepAlive(sandbox);
    await inject(sandbox, MODE === "break");
  }
  if (sandbox.errors?.length) console.log("infrastructure errors:", sandbox.errors);
} finally {
  await stopKeepAlive();
  if (env.KEEP) console.log(`KEEP set, leaving ${NAME}`);
  else await SandboxInstance.delete(NAME);
}
