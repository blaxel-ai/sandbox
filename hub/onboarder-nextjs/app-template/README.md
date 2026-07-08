# Blaxel Sandbox Lab

This app is the `blaxel/onboarder-nextjs:latest` sandbox image, a platform-managed template used by the onboarder's first-sandbox flow (see `HELLO_WORLD_IMAGE` in controlplane). It is not the general-purpose `blaxel/nextjs` template — it's hidden from the template picker. It is intentionally Blaxel-native: it gives agents a safe runtime with files, processes, a live system fingerprint, and docs links into the broader Blaxel platform.

Run locally inside the image with:

```bash
npm run dev -- --port 3000 --hostname 0.0.0.0
```

## Continue from here

Agents opening this sandbox in Codex, Claude Code, or Cursor auto-load `AGENTS.md` at this root — read that first if you're an agent; it's the short version of this section.

- Start editing the page in `app/components/`.
- The top-level route is `app/page.tsx`; it renders `SandboxLabPage` from `app/components/sandbox-lab-page.tsx`.
- Agent launch prompts live in `app/lib/agent-prompt.ts`.
- Platform/use-case docs links live in `app/lib/platform-links.ts`.
- Safe terminal commands live in `app/lib/sandbox-commands.ts`; the executable allowlist is enforced in `app/api/run-check/route.ts`.
- The live system fingerprint lives in `app/components/sandbox-fingerprint.tsx`, polling `app/api/sandbox-info/route.ts`.
- The demo artifact file is created by `scripts/create-artifact.mjs` under `/tmp/blaxel-sandbox-lab`.

## Useful terminal commands

The in-page terminal accepts the six safe sandbox commands from `app/lib/sandbox-commands.ts`, plus browser helpers:

- `copy` — copy the default agent prompt
- `clear` — clear the terminal
- `codex`, `claude`, `cursor` — copy the prompt and open that agent launcher

## Useful routes

- `/api/health` — lightweight app readiness
- `/api/sandbox-info` — live hostname, pid, uptime, memory, load average, and a running request counter, proving this is one continuously running process
- `/api/run-check` — allowlisted system checks

## Useful checks

Run these before sharing a changed template:

```bash
npm run sandbox-check
npm run typecheck
npm run build
```

## Add your own proof

A good Blaxel sandbox starter should show inspectable evidence, not just a transcript. The "Sandbox stats" panel is the primary "this is real" moment: it polls live process/OS facts (host, pid, uptime, memory, load average) every second, ticking smoothly between polls, because they only make sense for one continuously running process — a static export or a fresh serverless invocation can't produce them.

If you add a new capability demo:

1. Add a safe command label to `app/lib/sandbox-commands.ts`.
2. Add the executable mapping to `app/api/run-check/route.ts`.
3. Keep commands allowlisted — never execute arbitrary visitor input.
4. Update `scripts/sandbox-check.mjs` if the new proof should be part of the default verification loop.
