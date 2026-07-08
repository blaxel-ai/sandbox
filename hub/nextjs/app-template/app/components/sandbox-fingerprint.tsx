'use client';

import { useEffect, useRef, useState } from 'react';
import type { SandboxFingerprintData } from './types';

const POLL_INTERVAL_MS = 1000;
const TICK_INTERVAL_MS = 200;

function formatDuration(totalSeconds: number) {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = Math.floor(totalSeconds % 60);
  const parts = hours > 0 ? [hours, minutes, seconds] : [minutes, seconds];

  return parts.map((part) => String(part).padStart(2, '0')).join(':');
}

export function SandboxFingerprint() {
  const [info, setInfo] = useState<SandboxFingerprintData | null>(null);
  const [displayUptime, setDisplayUptime] = useState(0);
  const [isLive, setIsLive] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pollIntervalRef = useRef<number | null>(null);
  const tickIntervalRef = useRef<number | null>(null);
  const baseUptimeRef = useRef(0);
  const baseFetchedAtRef = useRef(0);

  useEffect(() => {
    let cancelled = false;

    async function poll() {
      try {
        const response = await fetch('/api/sandbox-info', { cache: 'no-store' });
        if (!response.ok) throw new Error(`Request failed with ${response.status}`);

        const data = (await response.json()) as SandboxFingerprintData;
        if (cancelled) return;

        baseUptimeRef.current = data.uptimeSeconds;
        baseFetchedAtRef.current = performance.now();
        setInfo(data);
        setError(null);
      } catch (fetchError) {
        if (!cancelled) {
          setError(
            fetchError instanceof Error ? fetchError.message : String(fetchError),
          );
        }
      }
    }

    void poll();

    if (isLive) {
      pollIntervalRef.current = window.setInterval(() => void poll(), POLL_INTERVAL_MS);
      tickIntervalRef.current = window.setInterval(() => {
        const elapsed = (performance.now() - baseFetchedAtRef.current) / 1000;
        setDisplayUptime(baseUptimeRef.current + Math.max(0, elapsed));
      }, TICK_INTERVAL_MS);
    }

    return () => {
      cancelled = true;
      if (pollIntervalRef.current) window.clearInterval(pollIntervalRef.current);
      if (tickIntervalRef.current) window.clearInterval(tickIntervalRef.current);
    };
  }, [isLive]);

  const memoryUsedPct = info
    ? Math.min(100, Math.round((info.rssMemoryMb / info.totalMemoryMb) * 100 * 8))
    : 0;

  return (
    <article className="card fingerprint-card">
      <div className="card-title with-action">
        <div>
          <span className="icon">◎</span>
          <div>
            <h2>Sandbox stats</h2>
            <p>Live process data from this sandbox, updating every second.</p>
          </div>
        </div>
        <button
          className="secondary live-toggle"
          onClick={() => setIsLive((value) => !value)}
          type="button"
        >
          <span className={`live-dot${isLive ? ' live-dot-on' : ''}`} />
          {isLive ? 'Live' : 'Paused'}
        </button>
      </div>

      {error ? (
        <p className="fingerprint-error">Couldn&apos;t reach this sandbox: {error}</p>
      ) : null}

      <div className="fingerprint-strip">
        <div className="fingerprint-chip">
          <span>Host</span>
          <strong>{info?.hostname ?? '—'}</strong>
        </div>
        <div className="fingerprint-chip">
          <span>PID</span>
          <strong>{info ? info.pid : '—'}</strong>
        </div>
        <div className="fingerprint-chip fingerprint-chip-wide">
          <span>Uptime</span>
          <strong>{info ? formatDuration(displayUptime) : '—'}</strong>
        </div>
        <div className="fingerprint-chip fingerprint-chip-wide">
          <span>Memory</span>
          <strong>{info ? `${info.rssMemoryMb} MB` : '—'}</strong>
          <div className="fingerprint-bar" aria-hidden="true">
            <div
              className="fingerprint-bar-fill"
              style={{ width: `${memoryUsedPct}%` }}
            />
          </div>
        </div>
        <div className="fingerprint-chip">
          <span>Load</span>
          <strong>{info ? info.loadAverage[0] : '—'}</strong>
        </div>
      </div>
    </article>
  );
}
