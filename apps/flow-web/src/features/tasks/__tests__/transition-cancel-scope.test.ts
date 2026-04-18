/**
 * Verify that useTransitionTask scopes cancelQueries to the affected
 * project list and task detail, not the entire tasksKeys.all.
 *
 * Bug fixed: onMutate cancelled all ['tasks'] queries, but onSettled
 * only re-fetched the specific project list. Other projects' in-flight
 * queries were silently killed and never recovered.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('useTransitionTask cancelQueries scope', () => {
  const source = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');

  const fnStart = source.indexOf('export function useTransitionTask()');
  const fnEnd = source.indexOf('\n}', fnStart) + 2;
  const fnBody = source.slice(fnStart, fnEnd);

  it('onMutate does NOT cancel tasksKeys.all', () => {
    // Extract the onMutate block
    const mutateStart = fnBody.indexOf('onMutate');
    const errorStart = fnBody.indexOf('onError');
    const onMutateBody = fnBody.slice(mutateStart, errorStart);

    // Should not contain `cancelQueries({ queryKey: tasksKeys.all })`
    // which would cancel all task queries across all projects.
    const bareAllCancel = /cancelQueries\(\{\s*queryKey:\s*tasksKeys\.all\s*\}/;
    expect(onMutateBody).not.toMatch(bareAllCancel);
  });

  it('onMutate cancels scoped to project list when projectId given', () => {
    const mutateStart = fnBody.indexOf('onMutate');
    const errorStart = fnBody.indexOf('onError');
    const onMutateBody = fnBody.slice(mutateStart, errorStart);

    // Should scope cancel to [...tasksKeys.all, 'list', vars.projectId]
    expect(onMutateBody).toContain("'list'");
    expect(onMutateBody).toContain('vars.projectId');
  });

  it('onMutate cancels the specific task detail', () => {
    const mutateStart = fnBody.indexOf('onMutate');
    const errorStart = fnBody.indexOf('onError');
    const onMutateBody = fnBody.slice(mutateStart, errorStart);

    expect(onMutateBody).toContain('tasksKeys.detail(vars.id)');
  });
});
