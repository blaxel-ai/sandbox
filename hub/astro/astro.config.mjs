// @ts-check
import { defineConfig } from 'astro/config';

// Astro's dev middleware builds `new URL('http://' + req.headers.host + …)`,
// which throws "Invalid URL" when a client reaches the dual-stack server over
// IPv6 and sends a bare (unbracketed) IPv6 Host header (e.g. "::1:4321").
// Normalize those so probes can't crash the dev server with unhandled rejections.
function normalizeIpv6Host() {
  return {
    name: 'blaxel-normalize-ipv6-host',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const host = req.headers.host;
        if (host && !host.startsWith('[') && (host.match(/:/g) || []).length > 1) {
          req.headers.host = 'localhost';
        }
        next();
      });
    },
  };
}

// https://astro.build/config
export default defineConfig({
  server: {
    host: '::',
    port: 4321,
    allowedHosts: true
  },
  vite: {
    plugins: [normalizeIpv6Host()]
  }
});
