export const SANDBOX_COMMANDS = [
  {
    id: 'sandbox-check',
    command: 'npm run sandbox-check',
    label: 'Full sandbox check',
  },
  { id: 'node-version', command: 'node -v', label: 'Check runtime' },
  { id: 'pwd', command: 'pwd', label: 'Show app path' },
  { id: 'list-files', command: 'ls -la', label: 'Inspect files' },
  {
    id: 'create-artifact',
    command: 'node scripts/create-artifact.mjs',
    label: 'Create proof artifact',
  },
  { id: 'typecheck', command: 'npm run typecheck', label: 'Verify types' },
] as const;

export const TERMINAL_BROWSER_COMMANDS = [
  { command: 'copy', label: 'Copy the agent prompt to your clipboard' },
  { command: 'clear', label: 'Clear the terminal' },
  { command: 'codex', label: 'Copy the prompt and open Codex' },
  { command: 'claude', label: 'Copy the prompt and open Claude' },
  { command: 'cursor', label: 'Copy the prompt and open Cursor' },
] as const;

export type SandboxCommand = (typeof SANDBOX_COMMANDS)[number];
export type CommandId = SandboxCommand['id'];
export type TerminalBrowserCommand =
  (typeof TERMINAL_BROWSER_COMMANDS)[number]['command'];

export function findSandboxCommandByCommand(command: string) {
  return SANDBOX_COMMANDS.find((candidate) => candidate.command === command);
}

export function findTerminalBrowserCommand(command: string) {
  return TERMINAL_BROWSER_COMMANDS.find(
    (candidate) => candidate.command === command.toLowerCase(),
  );
}

export function buildTerminalHelpOutput() {
  return [
    'This public demo terminal is allowlisted for safety.',
    '',
    'Live sandbox commands:',
    ...SANDBOX_COMMANDS.map(
      (command) => `  ${command.command}  # ${command.label}`,
    ),
    '',
    'Browser commands:',
    ...TERMINAL_BROWSER_COMMANDS.map(
      (command) => `  ${command.command}  # ${command.label}`,
    ),
    '',
    'For anything else, hand the command to your agent. Cursor, Claude, or Codex can use sandbox tools to run it and return proof.',
  ].join('\n');
}
