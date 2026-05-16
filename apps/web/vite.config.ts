import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import fs from 'fs';
import path from 'path';

const distDir = path.resolve(__dirname, '../server/internal/webui/dist');

// mkcert-generated cert lives at <repo>/certs/. Both the Go server and Vite
// reuse the same files so the SPA at https://localhost:3000 and the API at
// https://localhost:8443 share a trust root. When the files are missing (e.g.
// `vite build` in CI without certs), fall back to HTTP — the dev server will
// then mismatch tsio's Secure cookies, which is the expected nudge to run
// `make dev` (or `make certs`).
const certPath = path.resolve(__dirname, '../../certs/localhost.pem');
const keyPath = path.resolve(__dirname, '../../certs/localhost-key.pem');
const httpsConfig =
  fs.existsSync(certPath) && fs.existsSync(keyPath)
    ? { cert: fs.readFileSync(certPath), key: fs.readFileSync(keyPath) }
    : undefined;

// Restores the committed .gitkeep that emptyOutDir wipes at the start of
// each build. .gitkeep holds dist/ open for //go:embed all:dist on fresh
// checkouts where no web bundle has been built yet.
const restoreGitkeep = {
  name: 'restore-gitkeep',
  closeBundle() {
    fs.writeFileSync(path.join(distDir, '.gitkeep'), '');
  },
};

// Proxy /api and /files to the Go server. `secure: false` skips cert
// verification on the Node side — the cert is mkcert-issued and trusted by
// the OS, but Node uses its bundled CA list and won't see it without
// NODE_EXTRA_CA_CERTS. Disabling verification here is local-dev-only and the
// browser still validates the cert on https://localhost:3000.
const apiProxy = {
  target: 'https://localhost:8443',
  changeOrigin: false,
  secure: false,
};

export default defineConfig({
  plugins: [react(), restoreGitkeep],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 3000,
    https: httpsConfig,
    proxy: {
      '/api': apiProxy,
      '/files': apiProxy,
    },
  },
  preview: {
    port: 3000,
    https: httpsConfig,
  },
  build: {
    outDir: '../server/internal/webui/dist',
    emptyOutDir: true,
    sourcemap: true,
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test_setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
