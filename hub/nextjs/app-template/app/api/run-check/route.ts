import { NextResponse } from 'next/server';
import { execFile } from 'node:child_process';
import path from 'node:path';
import { promisify } from 'node:util';
import {
  SANDBOX_COMMANDS,
  type CommandId,
} from '../../lib/sandbox-commands';

export const runtime = 'nodejs';

const execFileAsync = promisify(execFile);

const appRoot = process.cwd();

type CommandSpec = {
  command: string;
  file: string;
  args?: string[];
  timeout?: number;
};

const COMMANDS: Record<CommandId, CommandSpec> = {
  'sandbox-check': {
    command: 'npm run sandbox-check',
    file: 'npm',
    args: ['run', 'sandbox-check'],
    timeout: 25000,
  },
  'node-version': {
    command: 'node -v',
    file: 'node',
    args: ['-v'],
  },
  pwd: {
    command: 'pwd',
    file: 'pwd',
  },
  'list-files': {
    command: 'ls -la',
    file: 'ls',
    args: ['-la', appRoot],
  },
  'create-artifact': {
    command: 'node scripts/create-artifact.mjs',
    file: 'node',
    args: [path.join(appRoot, 'scripts/create-artifact.mjs')],
  },
  typecheck: {
    command: 'npm run typecheck',
    file: 'npm',
    args: ['run', 'typecheck'],
    timeout: 20000,
  },
};

async function runCommand(id: CommandId) {
  const spec = COMMANDS[id];
  const startedAt = new Date().toISOString();

  try {
    const { stdout, stderr } = await execFileAsync(spec.file, spec.args ?? [], {
      cwd: appRoot,
      timeout: spec.timeout ?? 8000,
    });

    return {
      id,
      command: spec.command,
      completedAt: new Date().toISOString(),
      ok: true,
      output: (stdout || stderr || 'ok').trim(),
      startedAt,
    };
  } catch (error) {
    return {
      id,
      command: spec.command,
      completedAt: new Date().toISOString(),
      ok: false,
      output: error instanceof Error ? error.message : String(error),
      startedAt,
    };
  }
}

export async function POST(request: Request) {
  const body = await request.json().catch(() => ({}));
  const commandId = body.commandId as CommandId | undefined;

  if (!commandId || !(commandId in COMMANDS)) {
    return NextResponse.json(
      {
        error: 'unsupported command',
        supportedCommands: SANDBOX_COMMANDS.map((command) => command.id),
      },
      { status: 400 },
    );
  }

  return NextResponse.json({ result: await runCommand(commandId) });
}
