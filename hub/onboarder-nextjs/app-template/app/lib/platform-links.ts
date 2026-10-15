export const PLATFORM_PRIMITIVES = [
  ['Sandbox', 'https://docs.baseten.co/sandboxes/overview'],
  ['Model APIs', 'https://docs.baseten.co/inference/model-apis/overview'],
  ['Training', 'https://docs.baseten.co/training/index'],
  ['Observability', 'https://docs.baseten.co/observability/metrics'],
] as const;

export const SANDBOX_USE_CASES = [
  {
    href: 'https://docs.baseten.co/sandboxes/overview',
    icon: 'terminal',
    text: 'Let agents install dependencies, edit files, and run checks away from developer laptops.',
    title: 'Agent coding workspaces',
  },
  {
    href: 'https://docs.baseten.co/sandboxes/manage#access-an-http-service',
    icon: 'globe',
    text: 'Recreate customer issues, run the app, and hand teammates a live URL instead of a transcript.',
    title: 'Live repros and previews',
  },
  {
    href: 'https://docs.baseten.co/sandboxes/manage#interact-with-a-running-sandbox',
    icon: 'shield',
    text: 'Expose filesystem, process, and app tools to agents through controlled sandbox interfaces.',
    title: 'Safe tool execution',
  },
  {
    href: 'https://docs.baseten.co/sandboxes/manage-images#create-a-custom-image',
    icon: 'database',
    text: 'Bake runtimes, tools, and starting files into a versioned image so every sandbox starts ready.',
    title: 'Reusable custom images',
  },
] as const;

export type SandboxUseCaseIcon = (typeof SANDBOX_USE_CASES)[number]['icon'];
