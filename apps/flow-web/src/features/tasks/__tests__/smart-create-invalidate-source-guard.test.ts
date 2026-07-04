/**
 * Source guard for M-11: smart-create apply must invalidate the shared task
 * list prefix, not only a project list plus `myInfinite`. The created parent
 * and subtasks can surface in filtered project lists, infinite project lists,
 * and cross-workspace "my tasks" lists.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('useApplySmartTask invalidation', () => {
  it('broadcasts the shared task-list prefix', () => {
    const source = readFileSync(resolve(__dirname, '../smart-create-api.ts'), 'utf8');
    const onSuccess = source.match(/onSuccess:[\s\S]*?\n {4}},\n {2}}\);/)?.[0] ?? '';

    expect(onSuccess).toContain("queryKey: [...tasksKeys.all, 'list']");
    expect(onSuccess).not.toContain('tasksKeys.myInfinite()');
    expect(onSuccess).not.toContain("queryKey: [...tasksKeys.all, 'list', vars.projectId]");
  });
});
