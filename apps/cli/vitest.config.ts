import { defineConfig } from 'vitest/config';

/**
 * Vitest configuration for the `tnk` CLI package.
 *
 * Tests live in `tests/` and exercise the pure builders + executors in
 * `src/task-builders.ts` against a mocked SDK client. The CLI parser
 * itself (`@libraz/node-cli`) is intentionally not under test here —
 * unit tests assert request shapes, not argv parsing.
 */
export default defineConfig({
  test: {
    environment: 'node',
    include: ['tests/**/*.test.ts'],
    pool: 'threads',
  },
});
