'use client';

import { type FormEvent, useEffect, useRef, useState } from 'react';
import { AGENT_PROMPT, CUSTOMIZE_PROMPT } from '../lib/agent-prompt';
import {
  buildTerminalHelpOutput,
  findSandboxCommandByCommand,
  findTerminalBrowserCommand,
  SANDBOX_COMMANDS,
  type SandboxCommand,
} from '../lib/sandbox-commands';
import {
  agentLaunchTargets,
  AgentLauncher,
  type AgentLaunchTarget,
} from './agent-launcher';
import { Hero } from './hero';
import { PlatformStrip } from './platform-strip';
import { SandboxFingerprint } from './sandbox-fingerprint';
import { SandboxTerminal } from './sandbox-terminal';
import { UseCaseCards } from './use-case-cards';
import type { RunCommandResult, TerminalEntry } from './types';
import type { AgentLaunchTargetKey } from '../lib/agent-prompt';

const LAUNCH_STATUS_DURATION_MS = 6000;
const FALLBACK_COPY_FEEDBACK_MS = 1500;

export function SandboxLabPage() {
  const [terminalEntries, setTerminalEntries] = useState<TerminalEntry[]>([
    {
      command: '',
      id: 'welcome',
      output:
        'Blaxel sandbox ready. Click a safe command, or type help to see what this public terminal can run.',
      status: 'done',
    },
  ]);
  const [runningCommand, setRunningCommand] =
    useState<SandboxCommand['id'] | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [terminalInput, setTerminalInput] = useState('');
  const [launchedTarget, setLaunchedTarget] =
    useState<AgentLaunchTargetKey | null>(null);
  const [fallbackCopied, setFallbackCopied] = useState(false);
  const terminalViewportRef = useRef<HTMLDivElement | null>(null);
  const terminalInputRef = useRef<HTMLInputElement | null>(null);
  const launchStatusTimeoutRef = useRef<number | null>(null);
  const fallbackCopyTimeoutRef = useRef<number | null>(null);

  useEffect(() => {
    const viewport = terminalViewportRef.current;
    if (!viewport) return;

    requestAnimationFrame(() => {
      viewport.scrollTo({ top: viewport.scrollHeight, behavior: 'smooth' });
    });
  }, [terminalEntries]);

  useEffect(() => {
    return () => {
      if (launchStatusTimeoutRef.current) {
        window.clearTimeout(launchStatusTimeoutRef.current);
      }
      if (fallbackCopyTimeoutRef.current) {
        window.clearTimeout(fallbackCopyTimeoutRef.current);
      }
    };
  }, []);

  async function copy(value: string, key: string) {
    await navigator.clipboard.writeText(value);
    setCopied(key);
    window.setTimeout(() => setCopied(null), 1600);
  }

  function showLaunchStatus(target: AgentLaunchTargetKey) {
    if (launchStatusTimeoutRef.current) {
      window.clearTimeout(launchStatusTimeoutRef.current);
    }
    setLaunchedTarget(target);
    launchStatusTimeoutRef.current = window.setTimeout(() => {
      setLaunchedTarget(null);
      launchStatusTimeoutRef.current = null;
    }, LAUNCH_STATUS_DURATION_MS);
  }

  async function launchAgent(target: AgentLaunchTarget) {
    await navigator.clipboard.writeText(AGENT_PROMPT);
    showLaunchStatus(target.key);
    window.setTimeout(() => {
      window.location.assign(target.href);
    }, 80);
  }

  function showFallbackCopyStatus() {
    if (fallbackCopyTimeoutRef.current) {
      window.clearTimeout(fallbackCopyTimeoutRef.current);
    }
    setFallbackCopied(true);
    fallbackCopyTimeoutRef.current = window.setTimeout(() => {
      setFallbackCopied(false);
      fallbackCopyTimeoutRef.current = null;
    }, FALLBACK_COPY_FEEDBACK_MS);
  }

  async function handleFallbackCopy() {
    await navigator.clipboard.writeText(AGENT_PROMPT);
    showFallbackCopyStatus();
  }

  function focusTerminalInput() {
    terminalInputRef.current?.focus();
  }

  function appendDoneEntry(command: string, ok: boolean, output: string) {
    setTerminalEntries((entries) => [
      ...entries,
      {
        command,
        id: `manual-${Date.now()}`,
        ok,
        output,
        status: 'done',
      },
    ]);
  }

  async function copyPromptFromTerminal(command: string) {
    try {
      await navigator.clipboard.writeText(AGENT_PROMPT);
      showFallbackCopyStatus();
      appendDoneEntry(
        command,
        true,
        'Agent prompt copied to clipboard. Paste it into any agent, or type codex, claude, or cursor to open one.',
      );
    } catch (error) {
      appendDoneEntry(
        command,
        false,
        `Clipboard copy failed: ${
          error instanceof Error ? error.message : String(error)
        }`,
      );
    }
  }

  async function openAgentFromTerminal(
    command: string,
    target: AgentLaunchTarget,
  ) {
    let copiedPrompt = true;

    try {
      await navigator.clipboard.writeText(AGENT_PROMPT);
    } catch {
      copiedPrompt = false;
    }

    showLaunchStatus(target.key);
    appendDoneEntry(
      command,
      true,
      copiedPrompt
        ? `Opening ${target.label}. Agent prompt copied to clipboard.`
        : `Opening ${target.label}. Clipboard copy was blocked, but the launch URL includes the prompt.`,
    );
    window.setTimeout(() => {
      window.location.assign(target.href);
    }, 80);
  }

  function handleTerminalSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const command = terminalInput.trim();
    if (!command) return;

    const normalizedCommand = command.toLowerCase();

    if (normalizedCommand === 'help') {
      appendDoneEntry(command, true, buildTerminalHelpOutput());
      setTerminalInput('');
      return;
    }

    const browserCommand = findTerminalBrowserCommand(normalizedCommand);

    if (browserCommand) {
      setTerminalInput('');

      if (browserCommand.command === 'clear') {
        setTerminalEntries([]);
        return;
      }

      if (browserCommand.command === 'copy') {
        void copyPromptFromTerminal(command);
        return;
      }

      const agentTarget = agentLaunchTargets.find(
        (target) => target.key === browserCommand.command,
      );

      if (agentTarget) {
        void openAgentFromTerminal(command, agentTarget);
        return;
      }
    }

    const controlledCommand = findSandboxCommandByCommand(command);

    if (controlledCommand) {
      setTerminalInput('');
      void runCommand(controlledCommand);
      return;
    }

    appendDoneEntry(
      command,
      false,
      'Command not run. For security reasons, this public demo terminal only runs a small allowlist. Type help to see supported commands, or give this command to your agent so it can use sandbox tools to run it, inspect files, and return proof.',
    );
    setTerminalInput('');
  }

  async function runCommand(command: SandboxCommand) {
    const entryId = `${command.id}-${Date.now()}`;
    setRunningCommand(command.id);
    setTerminalEntries((entries) => [
      ...entries,
      {
        command: command.command,
        id: entryId,
        output: 'running…',
        status: 'running',
      },
    ]);

    let result: RunCommandResult;

    try {
      const response = await fetch('/api/run-check', {
        body: JSON.stringify({ commandId: command.id }),
        headers: { 'Content-Type': 'application/json' },
        method: 'POST',
      });

      if (!response.ok) {
        throw new Error(`Request failed with ${response.status}`);
      }

      const data = (await response.json()) as { result?: RunCommandResult };
      result = data.result ?? {
        command: command.command,
        ok: false,
        output: 'Command failed before producing output.',
      };
    } catch (error) {
      result = {
        command: command.command,
        ok: false,
        output: error instanceof Error ? error.message : String(error),
      };
    }

    setTerminalEntries((entries) =>
      entries.map((entry) =>
        entry.id === entryId
          ? {
              command: result.command,
              id: entry.id,
              ok: result.ok,
              output: result.output,
              status: 'done',
            }
          : entry,
      ),
    );
    setRunningCommand(null);
  }

  return (
    <main className="shell">
      <header className="topbar">
        <div className="brand">
          <img src="/logo_white.png" alt="Blaxel" />
        </div>
        <div className="topbar-meta">
          <span className="status-pill"><span /> sandbox online</span>
          <span>Next.js · port 3000</span>
        </div>
      </header>

      <Hero
        copied={copied}
        isBusy={Boolean(runningCommand)}
        onCopyAgentPrompt={() => void copy(AGENT_PROMPT, 'prompt')}
        onCopyCustomizePrompt={() => void copy(CUSTOMIZE_PROMPT, 'customize')}
        onRunSandboxCheck={() => void runCommand(SANDBOX_COMMANDS[0])}
      />

      <PlatformStrip />

      <SandboxFingerprint />

      <section className="dashboard-grid">
        <SandboxTerminal
          commandButtons={SANDBOX_COMMANDS}
          entries={terminalEntries}
          inputRef={terminalInputRef}
          onFocusInput={focusTerminalInput}
          onInputChange={setTerminalInput}
          onRunCommand={(command) => void runCommand(command)}
          onSubmit={handleTerminalSubmit}
          runningCommand={runningCommand}
          terminalInput={terminalInput}
          viewportRef={terminalViewportRef}
        />

        <AgentLauncher
          fallbackCopied={fallbackCopied}
          launchedTarget={launchedTarget}
          onFallbackCopy={() => void handleFallbackCopy()}
          onLaunch={(target) => void launchAgent(target)}
        />
      </section>

      <UseCaseCards />
    </main>
  );
}
