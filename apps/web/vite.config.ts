import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import fs from 'fs';
import path from 'path';

const distDir = path.resolve(__dirname, '../server/internal/webui/dist');

// Restores the committed .gitkeep that emptyOutDir wipes at the start of
// each build. .gitkeep holds dist/ open for //go:embed all:dist on fresh
// checkouts where no web bundle has been built yet.
const restoreGitkeep = {
  name: 'restore-gitkeep',
  closeBundle() {
    fs.writeFileSync(path.join(distDir, '.gitkeep'), '');
  },
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
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/files': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  preview: {
    port: 3000,
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
