import { NextResponse } from 'next/server';
import os from 'node:os';

export const runtime = 'nodejs';

// Module-level state survives across requests only because this is one real,
// continuously running process — a static export or a fresh serverless
// invocation could not accumulate this counter or keep a stable PID.
let requestCount = 0;
const bootTimestamp = new Date().toISOString();

function firstEnv(...keys: string[]) {
  for (const key of keys) {
    const value = process.env[key];
    if (value) return value;
  }
  return '';
}

function bytesToMb(bytes: number) {
  return Math.round((bytes / (1024 * 1024)) * 10) / 10;
}

export async function GET() {
  requestCount += 1;

  const host = firstEnv('BL_PREVIEW_URL', 'NEXT_PUBLIC_BL_PREVIEW_URL', 'VERCEL_URL');
  const previewUrl = host
    ? host.startsWith('http') ? host : `https://${host}`
    : '';
  const memory = process.memoryUsage();
  // Some emulated runtimes (e.g. qemu-user cross-arch emulation) report
  // process.memoryUsage().rss as 0. Fall back to heap + external so the
  // panel never shows a number that undercuts its own "this is real" point.
  const rssBytes = memory.rss || memory.heapUsed + memory.external;

  // Real Blaxel sandboxes set BL_NAME (the sandbox's actual name) but do not
  // give the container a meaningful kernel hostname — `hostname`/os.hostname()
  // genuinely returns the literal string "(none)" there. Prefer BL_NAME (and
  // its BLAXEL_* alias) so the panel shows a real identifier instead of that
  // placeholder; only fall back to os.hostname() outside Blaxel (e.g. local
  // dev/Docker), and never surface the literal "(none)" placeholder itself.
  const osHostname = os.hostname();
  const sandboxName =
    firstEnv('BL_NAME', 'BLAXEL_NAME', 'BL_SANDBOX_NAME', 'BLAXEL_SANDBOX_NAME') ||
    (osHostname !== '(none)' ? osHostname : '');
  const hostname = sandboxName || (osHostname !== '(none)' ? osHostname : 'unavailable');

  return NextResponse.json({
    bootTimestamp,
    cwd: process.cwd(),
    freeMemoryMb: bytesToMb(os.freemem()),
    hostname,
    loadAverage: os.loadavg().map((value) => Math.round(value * 100) / 100),
    node: process.version,
    pid: process.pid,
    platform: `${process.platform}/${process.arch}`,
    previewUrl,
    region: firstEnv('BL_REGION', 'BLAXEL_REGION', 'REGION') || 'detected at runtime',
    requestCount,
    rssMemoryMb: bytesToMb(rssBytes),
    sandboxName: sandboxName || 'current sandbox',
    timestamp: new Date().toISOString(),
    totalMemoryMb: bytesToMb(os.totalmem()),
    uptimeSeconds: Math.round(process.uptime()),
    workspace: firstEnv('BL_WORKSPACE', 'BLAXEL_WORKSPACE', 'WORKSPACE') || 'current workspace',
  });
}
