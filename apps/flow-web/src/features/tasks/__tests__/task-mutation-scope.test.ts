/**
 * Verify that useUpdateTask and useDeleteTask invalidate scoped
 * query keys, not the overly-broad tasksKeys.all.
 *
 * Bug fixed: both hooks previously used `invalidateQueries({ queryKey:
 * tasksKeys.all })` which nuked every task sub-query (comments, actors,
 * attachments, etc.) across all projects. Now they scope to
 * [...tasksKeys.all, 'list'] and tasksKeys.detail(id) only.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('useUpdateTask invalidation scope', () => {
  const source = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');

  // Extract the useUpdateTask function body
  const fnStart = source.indexOf('export function useUpdateTask()');
  const fnEnd = source.indexOf('\n}', fnStart) + 2;
  const fnBody = source.slice(fnStart, fnEnd);

  it('invalidates the specific task detail', () => {
    expect(fnBody).toContain('tasksKeys.detail(vars.id)');
  });

  it('invalidates task lists (scoped to "list" segment)', () => {
    expect(fnBody).toContain("...tasksKeys.all, 'list'");
  });

  it('does NOT invalidate tasksKeys.all directly', () => {
    // tasksKeys.all by itself would match everything. The fix scopes
    // invalidation to [...tasksKeys.all, 'list'] which only matches
    // list queries, not detail/comments/actors/etc.
    const lines = fnBody.split('\n');
    for (const line of lines) {
      if (line.includes('invalidateQueries') && line.includes('tasksKeys.all')) {
        // Must also contain 'list' or 'detail' — not bare tasksKeys.all
        expect(
          line.includes("'list'") || line.includes('tasksKeys.detail'),
          `invalidation must be scoped, got: ${line.trim()}`,
        ).toBe(true);
      }
    }
  });
});

describe('useDeleteTask invalidation scope', () => {
  const source = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');

  const fnStart = source.indexOf('export function useDeleteTask()');
  const fnEnd = source.indexOf('\n}', fnStart) + 2;
  const fnBody = source.slice(fnStart, fnEnd);

  it('removes the deleted task detail from cache', () => {
    expect(fnBody).toContain('removeQueries');
    expect(fnBody).toContain('tasksKeys.detail');
  });

  it('invalidates task lists (scoped to "list" segment)', () => {
    expect(fnBody).toContain("...tasksKeys.all, 'list'");
  });

  it('does NOT invalidate tasksKeys.all directly', () => {
    const lines = fnBody.split('\n');
    for (const line of lines) {
      if (line.includes('invalidateQueries') && line.includes('tasksKeys.all')) {
        expect(line.includes("'list'"), `invalidation must be scoped, got: ${line.trim()}`).toBe(
          true,
        );
      }
    }
  });
});
