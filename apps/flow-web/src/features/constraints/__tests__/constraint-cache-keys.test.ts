/**
 * Verify that constraint mutation hooks use the correct query cache
 * key for invalidation.
 *
 * Bug fixed: useAddConstraint and useRemoveConstraint were using
 * ['tasks', taskId] which does NOT match tasksKeys.detail(taskId) =
 * ['tasks', 'detail', taskId], making the invalidation a no-op.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('constraint mutation cache invalidation', () => {
  // Read the source file to verify the hardcoded key literals.
  const source = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');

  it('useAddConstraint invalidates ["tasks", "detail", taskId]', () => {
    // The onSuccess callback must contain the correct 3-segment key.
    expect(source).toContain("['tasks', 'detail', taskId]");
    // And must NOT contain the old buggy 2-segment key.
    const buggyPattern = /invalidateQueries\(\{[^}]*queryKey:\s*\['tasks',\s*taskId\]/;
    expect(source).not.toMatch(buggyPattern);
  });

  it('useRemoveConstraint invalidates ["tasks", "detail", taskId]', () => {
    // Count occurrences of the correct key — should appear at least twice
    // (once for add, once for remove).
    const matches = source.match(/\['tasks', 'detail', taskId\]/g);
    expect(matches).not.toBeNull();
    expect(matches?.length).toBeGreaterThanOrEqual(2);
  });
});
