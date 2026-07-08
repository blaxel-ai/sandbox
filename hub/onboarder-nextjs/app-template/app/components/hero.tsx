'use client';

export function Hero({
  copied,
  isBusy,
  onCopyAgentPrompt,
  onCopyCustomizePrompt,
  onRunSandboxCheck,
}: {
  copied: string | null;
  isBusy: boolean;
  onCopyAgentPrompt: () => void;
  onCopyCustomizePrompt: () => void;
  onRunSandboxCheck: () => void;
}) {
  return (
    <section className="intro-card">
      <div>
        <img className="hero-watermark" src="/logo_short.png" alt="" />
        <div className="eyebrow-row">
          <p className="eyebrow">Blaxel Sandbox Lab</p>
          <span className="template-badge">editable template</span>
        </div>
        <h1>A disposable computer for your agent.</h1>
        <p className="intro-copy">
          Let agents install dependencies, run code, and serve previews here
          — then rework this page itself into whatever you&apos;re building.
        </p>
        <div className="proof-row" aria-label="Sandbox capabilities">
          <span>isolated runtime</span>
          <span>files + processes</span>
          <span>standby resume</span>
          <span>preview URL</span>
        </div>
        <div className="actions">
          <button
            className="primary"
            onClick={onRunSandboxCheck}
            disabled={isBusy}
            type="button"
          >
            Run sandbox check
          </button>
          <button className="secondary" onClick={onCopyAgentPrompt} type="button">
            {copied === 'prompt' ? 'Prompt copied' : 'Copy template prompt'}
          </button>
          <button
            className="secondary"
            onClick={onCopyCustomizePrompt}
            type="button"
          >
            {copied === 'customize'
              ? 'Rebuild prompt copied'
              : 'Copy rebuild prompt'}
          </button>
        </div>
      </div>
    </section>
  );
}
