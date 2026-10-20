// Dump the guest kernel config and module-loading settings of a sandbox, so a
// kernel module can be built against the exact kernel the sandboxes run.
//
//   BL_ENV=dev REGION=eu-dub-1 IMAGE=blaxel/base-image:latest OUT=./guest-kernel node probe-kernel.mjs
import { SandboxInstance } from "@blaxel/core";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const IMAGE = process.env.IMAGE ?? "blaxel/base-image:latest";
const REGION = process.env.REGION;
const NAME = process.env.NAME ?? `kernel-probe-${Date.now().toString(36)}`;
const OUT = process.env.OUT ?? "./guest-kernel";
const KEEP = process.env.KEEP === "1";

const sandbox = await SandboxInstance.createIfNotExists({
  name: NAME,
  image: IMAGE,
  memory: 2048,
  ...(REGION ? { region: REGION } : {}),
});
await sandbox.wait();
console.log(`sandbox ${NAME} ready`);

const run = async (command) => {
  const p = await sandbox.process.exec({ command, waitForCompletion: true, timeout: 120 });
  return (p.logs ?? "").trim();
};

try {
  mkdirSync(OUT, { recursive: true });
  const uname = await run("uname -a");
  const version = await run("cat /proc/version");
  const modinfo = await run(
    "sysctl kernel.modules_disabled; cat /proc/sys/kernel/tainted; cat /sys/module/*/version 2>/dev/null | head -1; ls /sys/module | head -50",
  );
  const config = await run("zcat /proc/config.gz | base64 -w0");
  writeFileSync(join(OUT, "uname.txt"), `${uname}\n${version}\n`);
  writeFileSync(join(OUT, "modules.txt"), `${modinfo}\n`);
  writeFileSync(join(OUT, "config"), Buffer.from(config, "base64"));
  console.log(uname);
  console.log(version);
  console.log(modinfo);
  console.log(`config written to ${join(OUT, "config")}`);
} finally {
  if (!KEEP) await SandboxInstance.delete(NAME);
}
