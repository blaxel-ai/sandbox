import { SandboxInstance } from "@blaxel/core";
import fs from "fs";
import yargs from "yargs";
import { hideBin } from "yargs/helpers";

// Parse command line arguments
const argv = yargs(hideBin(process.argv))
  .command('$0', 'Run sandbox tests', (yargs) => {
    return yargs
      .option('mode', {
        describe: 'Sandbox mode',
        type: 'string',
        choices: ['local', 'get', 'create'],
        default: 'get',
        demandOption: false
      })
      .option('name', {
        describe: 'Sandbox name',
        type: 'string',
        default: 'custom-sandbox'
      })
      .option('image', {
        describe: 'Docker image for creating sandbox (only used with create mode)',
        type: 'string',
        default: 'blaxel/prod-base:latest'
      })
      .option('url', {
        describe: 'Force URL for sandbox (only used with local mode)',
        type: 'string',
        default: 'http://localhost:8080'
      });
  })
  .help()
  .argv;

const sandboxName = argv.name;

// curl http://localhost:8080/process -X POST -d '{"waitForCompletion": false, "command": "echo test"}'

async function getSandbox(mode, sandboxName, options = {}) {
  let sandbox;

  switch (mode) {
    case 'local':
      console.log(`Using local sandbox at ${options.url}`);
      sandbox = new SandboxInstance({
        name: sandboxName,
        forceUrl: options.url
      });
      break;

    case 'get':
      console.log(`Getting existing sandbox: ${sandboxName}`);
      sandbox = await SandboxInstance.get(sandboxName);
      break;

    case 'create':
      console.log(`Creating sandbox if not exists: ${sandboxName} with image: ${options.image}`);
      sandbox = await SandboxInstance.createIfNotExists({
        name: sandboxName,
        image: options.image
      });
      break;

    default:
      throw new Error(`Unknown mode: ${mode}`);
  }

  return sandbox;
}

async function main() {
  try {
    console.log(`Running in ${argv.mode} mode with sandbox: ${sandboxName}`);

    // Get sandbox based on mode
    const sandbox = await getSandbox(argv.mode, sandboxName, {
      url: argv.url,
      image: argv.image
    })

    try {
      const previewVsCodeS = await sandbox.previews.createIfNotExists({
        metadata: {
          name: "vscode"
        },
        spec: {
          port: 8081,
          public: true
        }
      })
      console.log("previewVsCodeS.url", previewVsCodeS.spec.url)
      const previewSandboxApiDebug =await sandbox.previews.createIfNotExists({
        metadata: {
          name: "debug-sandbox"
        },
        spec: {
          port: 8082,
          public: true
        }
      })
      console.log("previewSandboxApiDebug.url", previewSandboxApiDebug.spec.url)
    } catch {}


    // processes (execute in parallel, 20 at a time)
    const processesFileContents = fs.readFileSync("sandbox-processes.json", "utf8");
    const allProcesses = JSON.parse(processesFileContents);

    const runnableProcesses = allProcesses
      .filter((sandboxProcess) => sandboxProcess.name !== "code-server")
      .map((sandboxProcess) => ({
        ...sandboxProcess,
        waitForCompletion:
          sandboxProcess.waitForCompletion === undefined
            ? false
            : sandboxProcess.waitForCompletion,
      }));

    const createBatches = (items, batchSize) => {
      const batches = [];
      for (let i = 0; i < items.length; i += batchSize) {
        batches.push(items.slice(i, i + batchSize));
      }
      return batches;
    };

    const batchesOfTwenty = createBatches(runnableProcesses, 20);

    for (const batch of batchesOfTwenty) {
      await Promise.all(
        batch.map(async (sandboxProcess) => {
          try {
            const result = await sandbox.process.exec(sandboxProcess);
            console.log(`Process executed, name: ${result.name}`);
          } catch (execError) {
            console.error(
              `Process failed: ${sandboxProcess.name} =>`,
              execError
            );
          }
        })
      );
    }
  } catch (e) {
    console.error("There was an error => ", e);
  }
}

main()
  .catch((err) => {
    console.error("There was an error => ", err);
    process.exit(1);
  })
  .then(() => {
    process.exit(0);
  })
