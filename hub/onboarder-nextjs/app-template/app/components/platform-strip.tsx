import { LuExternalLink } from 'react-icons/lu';
import { PLATFORM_PRIMITIVES } from '../lib/platform-links';

export function PlatformStrip() {
  return (
    <section className="platform-strip" aria-label="Blaxel platform primitives">
      <div>
        <span className="launch-eyebrow">From sandbox to platform</span>
        <p>
          Start with a sandbox. Add shared files, hosted tools, agents, jobs,
          and observability when the workflow grows.
        </p>
      </div>
      <div className="platform-chain">
        {PLATFORM_PRIMITIVES.map(([label, href], index) => (
          <a href={href} key={label} rel="noreferrer noopener" target="_blank">
            <span>{label}</span>
            <LuExternalLink />
            {index < PLATFORM_PRIMITIVES.length - 1 ? (
              <i aria-hidden="true">→</i>
            ) : null}
          </a>
        ))}
      </div>
    </section>
  );
}
