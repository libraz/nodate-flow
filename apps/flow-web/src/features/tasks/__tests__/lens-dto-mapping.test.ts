/**
 * Verify the toLensDto mapper handles null/undefined fields correctly (M9).
 *
 * The toLensDto function is module-private (not exported), so we test its
 * behavior indirectly by reading the source code to ensure it:
 * - defaults filter to {} when null/undefined
 * - defaults sort to [] when null/undefined
 * - conditionally includes updatedAt only when non-null
 * - passes through groupBy (which can be null)
 *
 * We also verify the lensesKeys factory for query-key correctness.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

import { lensesKeys } from '../lens-api';

describe('toLensDto mapper (source analysis)', () => {
  const source = readFileSync(resolve(__dirname, '../lens-api.ts'), 'utf-8');

  // Extract the toLensDto function body
  const fnStart = source.indexOf('function toLensDto(');
  const fnEnd = source.indexOf('\n}', fnStart) + 2;
  const fnBody = source.slice(fnStart, fnEnd);

  it('defaults filter to empty object when nullish', () => {
    // lens.filter ?? {} ensures null/undefined becomes {}
    expect(fnBody).toContain('lens.filter ?? {}');
  });

  it('defaults sort to empty array when nullish', () => {
    // lens.sort ?? [] ensures null/undefined becomes []
    expect(fnBody).toContain('lens.sort ?? []');
  });

  it('conditionally includes updatedAt only when non-null', () => {
    // The mapper must check for null/undefined before setting updatedAt
    expect(fnBody).toContain('lens.updatedAt != null');
  });

  it('passes through groupBy directly (nullable)', () => {
    expect(fnBody).toContain('groupBy: lens.groupBy');
  });

  it('maps all required LensDto fields', () => {
    const requiredFields = [
      'id',
      'creatorId',
      'creatorDisplayName',
      'name',
      'filter',
      'sort',
      'groupBy',
      'isDefault',
      'sortWeight',
      'createdAt',
    ];
    for (const field of requiredFields) {
      expect(fnBody).toContain(`${field}:`);
    }
  });
});

describe('lensesKeys factory', () => {
  it('all key is ["lenses"]', () => {
    expect(lensesKeys.all).toEqual(['lenses']);
  });

  it('list key includes workspaceId', () => {
    const key = lensesKeys.list('ws-1');
    expect(key).toEqual(['lenses', 'ws-1', '']);
  });

  it('list key includes projectId when provided', () => {
    const key = lensesKeys.list('ws-1', 'proj-1');
    expect(key).toEqual(['lenses', 'ws-1', 'proj-1']);
  });

  it('list key uses empty string for missing projectId', () => {
    const key = lensesKeys.list('ws-1');
    expect(key[2]).toBe('');
  });

  it('list key is prefixed by all key', () => {
    const all = lensesKeys.all;
    const list = lensesKeys.list('ws-1');
    expect(list.slice(0, all.length)).toEqual(all);
    expect(list.length).toBeGreaterThan(all.length);
  });
});
