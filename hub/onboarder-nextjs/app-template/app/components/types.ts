import type { CommandId } from '../lib/sandbox-commands';

export type TerminalEntry = {
  command: string;
  id: string;
  ok?: boolean;
  output: string;
  status: 'done' | 'running';
};

export type RunCommandResult = {
  command: string;
  ok: boolean;
  output: string;
};

export type SandboxFingerprintData = {
  bootTimestamp: string;
  cwd: string;
  freeMemoryMb: number;
  hostname: string;
  loadAverage: number[];
  node: string;
  pid: number;
  platform: string;
  previewUrl: string;
  region: string;
  requestCount: number;
  rssMemoryMb: number;
  sandboxName: string;
  timestamp: string;
  totalMemoryMb: number;
  uptimeSeconds: number;
  workspace: string;
};

export type RunningCommand = CommandId | null;
