# Agent instructions — Blaxel Sandbox Lab

You're working inside `/blaxel/app`, a running Next.js dev server (port 3000, already started). This app is a **template, not a finished product**. Treat everything on the page — layout, terminal, stats panel, agent launcher, docs cards — as scaffolding you're free to change, extend, or delete. There is no "correct" final state to preserve.

## Orient fast

- `app/components/sandbox-lab-page.tsx` — top-level page, wires everything together.
- `app/components/` — one file per UI section (hero, terminal, agent launcher, stats, use-case cards).
- `app/lib/` — shared config: agent prompts, terminal command allowlist, docs links.
- `app/api/` — route handlers (`health`, `sandbox-info`, `run-check`).
- `README.md` — fuller human-facing guide to the same structure; read it if you want more detail than this file.

## The one real safety boundary

`app/lib/sandbox-commands.ts` + `app/api/run-check/route.ts` define a small server-side allowlist of commands the public terminal UI can execute. If you touch the terminal, keep new commands allowlisted server-side — never wire arbitrary visitor/browser input straight into `execFile`/`exec`. Everything else on the page has no such constraint; rework it freely.

## Before you call a change done

```bash
npm run typecheck
npm run sandbox-check
npm run build
```

`sandbox-check` runs typecheck plus a couple of real proof commands (node version, a sandbox file write). Keep it passing, or update it if you change what it should verify — it's your regression check, not a fixed spec.

## Make it yours

If the task is open-ended ("customize this," "rebuild this into X"), don't be conservative — replace whole sections, remove the terminal or stats panel if they don't fit, change the framing entirely. The only fixed points are: keep the app running on port 3000, keep the verification loop above passing, and don't wire unsafe execution into anything public-facing.
