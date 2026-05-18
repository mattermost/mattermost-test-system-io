import { defineConfig } from 'tsdown';

export default defineConfig({
  entry: ['src/index.ts'],
  format: ['cjs'],
  target: 'node24',
  clean: true,
  minify: false,
  sourcemap: false,
  splitting: false,
  noExternal: [/.*/],
});
