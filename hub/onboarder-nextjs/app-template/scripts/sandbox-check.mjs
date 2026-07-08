import { execFile } from 'node:child_process';
import os from 'node:os';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

async function run(label, file, args = []) {
  console.log(`$ ${label}`);

  const { stdout, stderr } = await execFileAsync(file, args, {
    cwd: process.cwd(),
    timeout: 20000,
  });
  const output = [stdout, stderr].filter(Boolean).join('').trim();

  if (output) {
    console.log(output);
  }
}

console.log('Sandbox Lab runtime check');
console.log(`hostname ${os.hostname()}`);
console.log(`platform ${process.platform}/${process.arch}`);
console.log(`cwd ${process.cwd()}`);
console.log('');

await run('node -v', 'node', ['-v']);
await run('node scripts/create-artifact.mjs', 'node', [
  'scripts/create-artifact.mjs',
]);
await run('npm run typecheck', 'npm', ['run', 'typecheck']);

console.log('All sandbox checks passed.');
