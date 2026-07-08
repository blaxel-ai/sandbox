'use client';

import { LuCopy, LuCopyCheck } from 'react-icons/lu';
import { SiClaude, SiOpenai } from 'react-icons/si';
import type { IconType } from 'react-icons';
import {
  AGENT_PROMPT,
  buildAgentLaunchHref,
  type AgentLaunchTargetKey,
} from '../lib/agent-prompt';

export type AgentLaunchTarget = {
  className: string;
  href: string;
  icon: IconType;
  key: AgentLaunchTargetKey;
  label: string;
};

function CursorAgentIcon({ className }: { className?: string }) {
  return (
    <svg
      aria-hidden="true"
      className={className}
      fill="none"
      viewBox="0 0 24 24"
    >
      <path d="M4 3.5 20.5 12 4 20.5V14l7.25-2L4 10z" fill="currentColor" />
      <path d="M12.25 12 4 3.5v6.7z" fill="#111316" opacity="0.72" />
      <path d="M12.25 12 4 20.5v-6.7z" fill="#111316" opacity="0.42" />
    </svg>
  );
}

export const agentLaunchTargets: AgentLaunchTarget[] = [
  {
    className: 'agent-icon-codex',
    href: buildAgentLaunchHref('codex'),
    icon: SiOpenai,
    key: 'codex',
    label: 'Codex',
  },
  {
    className: 'agent-icon-claude',
    href: buildAgentLaunchHref('claude'),
    icon: SiClaude,
    key: 'claude',
    label: 'Claude',
  },
  {
    className: 'agent-icon-cursor',
    href: buildAgentLaunchHref('cursor'),
    icon: CursorAgentIcon,
    key: 'cursor',
    label: 'Cursor',
  },
];

export function AgentLauncher({
  fallbackCopied,
  launchedTarget,
  onFallbackCopy,
  onLaunch,
}: {
  fallbackCopied: boolean;
  launchedTarget: AgentLaunchTargetKey | null;
  onFallbackCopy: () => void;
  onLaunch: (target: AgentLaunchTarget) => void;
}) {
  return (
    <section className="agent-launch-actions">
      <div className="agent-launch-heading">
        <h2>Rework this template</h2>
        <p>
          Not a fixed dashboard — this whole page is yours to change. Opens
          your agent with the app ready to edit.
        </p>
      </div>
      <div className="agent-launch-grid" aria-label="Agent launch actions">
        {agentLaunchTargets.map((target) => {
          const Icon = target.icon;

          return (
            <a
              className={`agent-launch-button${
                launchedTarget === target.key ? ' copied' : ''
              }`}
              href={target.href}
              key={target.key}
              onClick={(event) => {
                event.preventDefault();
                onLaunch(target);
              }}
            >
              <Icon className={`agent-launch-icon ${target.className}`} />
              <span>Open in {target.label}</span>
            </a>
          );
        })}
      </div>
      <div aria-live="polite" className="launch-status-slot">
        {launchedTarget ? (
          <p className="launch-status">
            <LuCopyCheck />
            <span>
              Prompt copied — paste it into{' '}
              {
                agentLaunchTargets.find(
                  (target) => target.key === launchedTarget,
                )?.label
              }{' '}
              if it didn't open.
            </span>
          </p>
        ) : null}
      </div>
      <button
        className={`fallback-copy-button${fallbackCopied ? ' copied' : ''}`}
        onClick={onFallbackCopy}
        type="button"
      >
        {fallbackCopied ? <LuCopyCheck /> : <LuCopy />}
        <span>
          {fallbackCopied
            ? 'Prompt copied — paste it into your agent'
            : 'Using another agent? Copy the prompt — works anywhere'}
        </span>
      </button>
    </section>
  );
}

export { AGENT_PROMPT };
