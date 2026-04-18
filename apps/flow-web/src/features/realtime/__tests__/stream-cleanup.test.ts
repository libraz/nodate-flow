/**
 * Verify that useWorkspaceStream cleanup does not prematurely set
 * streamHealthy to true.
 *
 * Bug fixed: on unmount, setStreamHealthy(true) was called, which
 * briefly suppressed the polling fallback during workspace switches.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('useWorkspaceStream cleanup', () => {
  const source = readFileSync(resolve(__dirname, '../use-workspace-stream.ts'), 'utf-8');

  it('cleanup does not set streamHealthy to true', () => {
    // Find the cleanup return function
    const cleanupStart = source.indexOf('return () => {');
    expect(cleanupStart).toBeGreaterThan(-1);

    const cleanupEnd = source.indexOf('};', cleanupStart);
    const cleanupBody = source.slice(cleanupStart, cleanupEnd);

    // Must NOT contain setStreamHealthy(true) — that was the bug
    expect(cleanupBody).not.toContain('setStreamHealthy(true)');

    // Must still abort the controller and clear timers
    expect(cleanupBody).toContain('controller.abort()');
    expect(cleanupBody).toContain('clearTimeout');
  });
});
