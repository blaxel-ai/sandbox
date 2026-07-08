export const AGENT_PROMPT = `You are working in a Blaxel sandbox. The Next.js app in /blaxel/app (already running on port 3000) is a TEMPLATE, not a finished product — treat this whole page as an editable canvas, not a fixed dashboard.\n\nTask:\n1. Look at the current page and pick something worth changing — a small tweak or a bigger rework, your call.\n2. Make the change.\n3. Run npm run sandbox-check.\n4. Verify the public preview still works.\n5. Explain what changed and why.\n\nThis page can become anything. Make it yours.`;

export const CUSTOMIZE_PROMPT = `You are working in a Blaxel sandbox. The Next.js app in /blaxel/app (already running on port 3000) is a starter template — not the finished product.\n\nTask:\nRebuild this starter into a real product page for whatever I'm actually working on. Replace or remove anything that doesn't fit — the terminal, the stats panel, the layout are all optional scaffolding, not requirements. Keep the Blaxel proof loop intact: make the change, run npm run sandbox-check, and verify the preview still works.\n\nWhen you finish, report changed files, the verification command output, and the preview status.`;

export type AgentLaunchTargetKey = 'codex' | 'claude' | 'cursor';

export function buildAgentLaunchHref(
  agentKey: AgentLaunchTargetKey,
  prompt = AGENT_PROMPT,
) {
  const encodedPrompt = encodeURIComponent(prompt);

  switch (agentKey) {
    case 'codex':
      return `codex://new?prompt=${encodedPrompt}`;
    case 'claude':
      return `claude://code/new?q=${encodedPrompt}`;
    case 'cursor': {
      const url = new URL('https://cursor.com/link/prompt');
      url.searchParams.set('text', prompt);
      return url.toString();
    }
  }
}
