// Reproducer for the drive endpoint 5xx seen on workspace quest (PR #373).
//
//   unmount: DELETE /drives/mount/<path> on a path that was never mounted.
//            Bug: 500. Fixed: 404.
//   mount:   mount the same drive at /mnt/d1, /mnt/d2, ... and watch `df /`.
//            Bug: every blfs mount preallocates a 128 MB cache on the root
//            tmpfs (guest RAM), so a small sandbox fills up after a few mounts
//            and the next one fails with 5xx (500, or 504 when the VM dies). Fixed: / barely moves, all mounts
//            succeed (or a full disk gives 507).
//
// Usage (from scripts/, after `npm i`):
//   BL_ENV=dev BL_WORKSPACE=... BL_API_KEY=... node repro-drive-mount-unmount.mjs
// Env:
//   SCENARIO=unmount|mount|both  (default both)
//   SANDBOX_API_BINARY=<path>    linux/amd64 sandbox-api build to upgrade the
//                                sandbox to before running (the patched run);
//                                unset = stock image (the unpatched run)
//   IMAGE (blaxel/base-image:latest)  REGION (BL_REGION or us-was-1)
//   MEMORY in MB (2048)  MAX_MOUNTS (16)  KEEP=1 keeps sandboxes and drive
import { readFile } from "node:fs/promises";
import { DriveInstance, SandboxInstance, settings } from "@blaxel/core";

const SCENARIO = process.env.SCENARIO || "both";
const BINARY = process.env.SANDBOX_API_BINARY;
const IMAGE = process.env.IMAGE || "blaxel/base-image:latest";
const REGION = process.env.REGION || process.env.BL_REGION || "us-was-1";
const MEMORY = Number(process.env.MEMORY || 2048);
const MAX_MOUNTS = Number(process.env.MAX_MOUNTS || 16);
const KEEP = process.env.KEEP === "1";
const RUN = `${Date.now().toString(36)}`;
const LABELS = { purpose: "repro-drive-373" };

const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function health(sandbox) {
  const res = await fetch(`${sandbox.metadata.url}/health`, { headers: settings.headers });
  return res.ok ? res.json() : {};
}

// Serves the binary from inside the sandbox and points /upgrade at it.
async function upgrade(sandbox) {
  const version = `repro-${RUN}`;
  const before = (await health(sandbox)).upgradeCount ?? 0;
  log(`upgrading sandbox-api to ${BINARY}`);
  await sandbox.fs.writeBinary(`/tmp/upgrade/download/${version}/sandbox-api-linux-amd64`, await readFile(BINARY));
  await sandbox.process.exec({ name: "upgrade-server", command: "python3 -m http.server 8765 --directory /tmp/upgrade" });
  await sleep(1000);
  await sandbox.system.upgrade({ version, baseUrl: "http://localhost:8765" });
  for (let i = 0; i < 60; i++) {
    await sleep(1000);
    const h = await health(sandbox).catch(() => ({}));
    if ((h.upgradeCount ?? 0) > before) break;
    if (i === 59) throw new Error(`upgrade did not complete, health: ${JSON.stringify(h)}`);
  }
  await sandbox.process.exec({ command: "pkill -f 'http.server 8765'; rm -rf /tmp/upgrade", waitForCompletion: true });
  log(`sandbox-api now ${(await health(sandbox)).version}`);
}

async function createSandbox(name) {
  log(`creating sandbox ${name} (${IMAGE}, ${MEMORY} MB, ${REGION})`);
  const sandbox = await SandboxInstance.create({ name, image: IMAGE, memory: MEMORY, region: REGION, labels: LABELS });
  try {
    await sandbox.wait();
    log(`sandbox-api ${(await health(sandbox)).version}`);
    if (BINARY) await upgrade(sandbox);
  } catch (err) {
    if (!KEEP) await SandboxInstance.delete(name).catch(() => {});
    throw err;
  }
  return sandbox;
}

async function status(promise) {
  try {
    await promise;
    return 200;
  } catch (err) {
    return { status: err.status, message: err.message };
  }
}

async function rootDisk(sandbox) {
  const r = await sandbox.process.exec({ command: "df -m / | tail -1", waitForCompletion: true });
  const [, size, used, avail, pct] = r.logs.trim().split(/\s+/);
  return `/ ${used}/${size} MB used (${pct}), ${avail} MB free`;
}

async function unmountScenario() {
  const sandbox = await createSandbox(`repro-unmount-${RUN}`);
  let bug = false;
  try {
    for (const path of ["/mnt/never-mounted", "/tmp"]) {
      const res = await status(sandbox.drives.unmount(path));
      const code = typeof res === "number" ? res : res.status;
      log(`unmount ${path} -> ${code}${res.message ? ` ${res.message}` : ""}`);
      if (code >= 500) bug = true;
    }
  } finally {
    if (!KEEP) await SandboxInstance.delete(sandbox.metadata.name).catch(() => {});
  }
  log(bug ? "unmount: BUG REPRODUCED (5xx on a path with nothing mounted)" : "unmount: OK (4xx)");
  return bug;
}

async function mountScenario() {
  const driveName = `repro-drive-${RUN}`;
  log(`creating drive ${driveName}`);
  const drive = await DriveInstance.createIfNotExists({ name: driveName, region: REGION, labels: LABELS });
  const sandbox = await createSandbox(`repro-mount-${RUN}`);
  let bug = false;
  try {
    log(`before mounts: ${await rootDisk(sandbox)}`);
    for (let i = 1; i <= MAX_MOUNTS; i++) {
      const mountPath = `/mnt/d${i}`;
      const res = await status(sandbox.drives.mount({ driveName, mountPath, drivePath: "/" }));
      if (res !== 200) {
        log(`mount #${i} ${mountPath} -> ${res.status} ${res.message}`);
        log(`after failure: ${await rootDisk(sandbox)}`);
        bug = res.status >= 500 && res.status !== 507;
        if (!bug) log("mount: disk full reported as 507");
        break;
      }
      log(`mount #${i} ${mountPath} -> 200, ${await rootDisk(sandbox)}`);
    }
  } finally {
    if (!KEEP) {
      await SandboxInstance.delete(sandbox.metadata.name).catch(() => {});
      await sleep(5000);
      await drive.delete().catch((e) => log(`drive delete: ${e.message}`));
    }
  }
  log(bug ? "mount: BUG REPRODUCED (mounts fill / until a mount fails with 5xx)" : "mount: OK");
  return bug;
}

const results = [];
if (SCENARIO === "unmount" || SCENARIO === "both") results.push(await unmountScenario());
if (SCENARIO === "mount" || SCENARIO === "both") results.push(await mountScenario());
process.exit(results.some(Boolean) ? 1 : 0);
