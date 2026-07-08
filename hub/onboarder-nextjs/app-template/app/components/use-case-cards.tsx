import {
  LuDatabase,
  LuExternalLink,
  LuGlobe,
  LuShieldCheck,
  LuTerminal,
} from 'react-icons/lu';
import type { IconType } from 'react-icons';
import {
  SANDBOX_USE_CASES,
  type SandboxUseCaseIcon,
} from '../lib/platform-links';

const useCaseIcons: Record<SandboxUseCaseIcon, IconType> = {
  database: LuDatabase,
  globe: LuGlobe,
  shield: LuShieldCheck,
  terminal: LuTerminal,
};

export function UseCaseCards() {
  return (
    <section className="capabilities" aria-label="What teams use sandboxes for">
      {SANDBOX_USE_CASES.map((item) => {
        const Icon = useCaseIcons[item.icon];

        return (
          <a
            className="capability-card"
            href={item.href}
            key={item.title}
            rel="noreferrer noopener"
            target="_blank"
          >
            <div className="mini-icon">
              <Icon />
            </div>
            <div className="capability-copy">
              <div className="capability-title">
                <h3>{item.title}</h3>
                <LuExternalLink />
              </div>
              <p>{item.text}</p>
            </div>
          </a>
        );
      })}
    </section>
  );
}
