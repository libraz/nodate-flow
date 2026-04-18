/**
 * Verify that useUpdateMe has an onSettled handler that re-fetches
 * the profile to reconcile optimistic updates.
 *
 * Bug fixed: useUpdateMe had onMutate (optimistic) + onError (rollback)
 * + onSuccess (set from response), but no onSettled. Without onSettled,
 * concurrent PATCH calls could leave the cache with stale data.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('useUpdateMe mutation lifecycle', () => {
  const source = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');

  // Extract the useUpdateMe function body
  const fnStart = source.indexOf('export function useUpdateMe()');
  const fnBody = source.slice(fnStart, source.indexOf('\n}', fnStart) + 2);

  it('has onMutate for optimistic update', () => {
    expect(fnBody).toContain('onMutate');
  });

  it('has onError for rollback', () => {
    expect(fnBody).toContain('onError');
  });

  it('has onSuccess to set authoritative data', () => {
    expect(fnBody).toContain('onSuccess');
  });

  it('has onSettled to reconcile with server', () => {
    expect(fnBody).toContain('onSettled');
    expect(fnBody).toContain('invalidateQueries');
  });
});
