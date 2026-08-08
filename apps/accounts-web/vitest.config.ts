import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'happy-dom',
    globals: true,
    include: ['src/**/*.test.{ts,tsx}', 'tests/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.test.{ts,tsx}', 'src/**/__tests__/**'],
      // Thresholds are a ratchet on the measured number, not the
      // aspirational one. The convention's 80/80/75 target is a long way
      // off here, and a gate set above the current line has to be
      // switched off on the day it lands — which is how a project ends
      // up with a coverage config that gates nothing. Set at the floor
      // of what the suite covers today so a change that removes
      // coverage fails; raise these numbers whenever a run comes in
      // comfortably above them.
      thresholds: {
        statements: 45,
        branches: 41,
        functions: 40,
        lines: 46,
      },
    },
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
});
