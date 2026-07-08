export const PLATFORM_PRIMITIVES = [
  ['Sandbox', 'https://docs.blaxel.ai/Sandboxes/Overview'],
  ['Drive', 'https://docs.blaxel.ai/Agent-drive/Overview'],
  ['MCP', 'https://docs.blaxel.ai/Functions/Overview'],
  ['Agent', 'https://docs.blaxel.ai/Agents/Overview'],
  ['Jobs', 'https://docs.blaxel.ai/Jobs/Overview'],
  ['Observability', 'https://docs.blaxel.ai/Observability/Overview'],
] as const;

export const SANDBOX_USE_CASES = [
  {
    href: 'https://docs.blaxel.ai/Sandboxes/Overview',
    icon: 'terminal',
    text: 'Let agents install dependencies, edit files, and run checks away from developer laptops.',
    title: 'Agent coding workspaces',
  },
  {
    href: 'https://docs.blaxel.ai/Sandboxes/Preview-url',
    icon: 'globe',
    text: 'Recreate customer issues, run the app, and hand teammates a live URL instead of a transcript.',
    title: 'Live repros and previews',
  },
  {
    href: 'https://docs.blaxel.ai/Sandboxes/MCP',
    icon: 'shield',
    text: 'Expose filesystem, process, and app tools to agents through controlled sandbox interfaces.',
    title: 'Safe tool execution',
  },
  {
    href: 'https://docs.blaxel.ai/Agent-drive/Overview',
    icon: 'database',
    text: 'Keep context ephemeral by default, persistent with volumes, or shared through Agent Drive.',
    title: 'Persistent shared memory',
  },
] as const;

export type SandboxUseCaseIcon = (typeof SANDBOX_USE_CASES)[number]['icon'];
