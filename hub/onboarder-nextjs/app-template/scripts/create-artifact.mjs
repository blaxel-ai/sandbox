import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

const artifactDir = '/tmp/blaxel-sandbox-lab';
const artifactPath = path.join(artifactDir, 'demo-artifact.json');

const content = {
  createdBy: 'Blaxel sandbox',
  timestamp: new Date().toISOString(),
  message: 'Your agent can create, inspect, and modify files in this sandbox.',
  nextStep: 'Ask your agent to edit this app and verify the preview URL.',
};

await mkdir(artifactDir, { recursive: true });
await writeFile(artifactPath, JSON.stringify(content, null, 2));

console.log(`created ${artifactPath}`);
console.log(JSON.stringify(content, null, 2));
