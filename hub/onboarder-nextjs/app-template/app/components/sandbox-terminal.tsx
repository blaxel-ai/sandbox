'use client';

import type { FormEvent, RefObject } from 'react';
import type { SandboxCommand } from '../lib/sandbox-commands';
import type { RunningCommand, TerminalEntry } from './types';

export function SandboxTerminal({
  commandButtons,
  entries,
  inputRef,
  onFocusInput,
  onInputChange,
  onRunCommand,
  onSubmit,
  runningCommand,
  terminalInput,
  viewportRef,
}: {
  commandButtons: readonly SandboxCommand[];
  entries: TerminalEntry[];
  inputRef: RefObject<HTMLInputElement | null>;
  onFocusInput: () => void;
  onInputChange: (value: string) => void;
  onRunCommand: (command: SandboxCommand) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  runningCommand: RunningCommand;
  terminalInput: string;
  viewportRef: RefObject<HTMLDivElement | null>;
}) {
  return (
    <article className="card terminal-card">
      <div className="card-title with-action terminal-heading">
        <div>
          <span className="icon">›</span>
          <div>
            <h2>Live sandbox terminal</h2>
            <p>
              These are live commands. Click one and this app runs it inside the
              sandbox, then prints the result here.
            </p>
          </div>
        </div>
      </div>

      <div className="command-grid" aria-label="Live sandbox commands">
        {commandButtons.map((command) => (
          <button
            key={command.id}
            type="button"
            className="command-button"
            disabled={Boolean(runningCommand)}
            onClick={() => onRunCommand(command)}
          >
            <code>{command.label}</code>
            {runningCommand === command.id ? <span>running</span> : null}
          </button>
        ))}
      </div>

      <div className="terminal-shell" onClick={onFocusInput}>
        <div className="terminal-chrome" aria-hidden="true">
          <span className="chrome-dot chrome-red" />
          <span className="chrome-dot chrome-yellow" />
          <span className="chrome-dot chrome-green" />
          <strong>sandbox@blaxel</strong>
          <code>/blaxel/app</code>
        </div>
        <div className="terminal" aria-live="polite" ref={viewportRef}>
          {entries.map((entry) => (
            <div className="terminal-line" key={entry.id}>
              {entry.command ? (
                <div className="prompt-line">
                  <span
                    className={
                      entry.status === 'running'
                        ? 'dot running'
                        : entry.ok === false
                          ? 'dot fail'
                          : 'dot ok'
                    }
                  />
                  <span className="prompt-user">sandbox</span>
                  <span className="prompt-path">/blaxel/app</span>
                  <span className="prompt-symbol">$</span>
                  <code>{entry.command}</code>
                </div>
              ) : null}
              <pre>{entry.output}</pre>
              {entry.status === 'running' ? <span className="cursor" /> : null}
            </div>
          ))}
          <div className="terminal-line idle-terminal-line">
            <form
              className="prompt-line terminal-input-line terminal-input-form"
              onSubmit={onSubmit}
            >
              <span className="dot idle" />
              <span className="prompt-user">sandbox</span>
              <span className="prompt-path">/blaxel/app</span>
              <span className="prompt-symbol">$</span>
              <input
                aria-label="Type a command for your agent"
                disabled={Boolean(runningCommand)}
                ref={inputRef}
                onChange={(event) => onInputChange(event.target.value)}
                placeholder={
                  runningCommand
                    ? 'waiting for command…'
                    : 'type help or an allowed command…'
                }
                spellCheck={false}
                value={terminalInput}
              />
            </form>
          </div>
        </div>
      </div>
    </article>
  );
}
