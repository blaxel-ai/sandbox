// @ts-check
import { defineConfig } from 'astro/config';

// https://astro.build/config
export default defineConfig({
  server: {
    host: '::',
    port: 4321,
    allowedHosts: true
  }
});