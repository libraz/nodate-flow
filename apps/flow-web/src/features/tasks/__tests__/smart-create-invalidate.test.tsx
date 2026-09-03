/**
 * Applying a smart-create proposal must invalidate the shared
 * task-list prefix, not just the current project's list plus
 * `myInfinite`. The created parent and subtasks can surface in
 * filtered project lists, infinite project lists for the SAME
 * project, list queries for OTHER projects (assignees may belong to
 * multiple projects' boards), and the cross-workspace "my tasks"
 * list.
 *
 * This seeds a real QueryClient with fresh (non-invalidated) queries
 * under each of those keys plus one unrelated `detail` query, runs the
 * mutation, and asserts every list-prefixed query was invalidated
 * while the detail query was left alone. A regression to invalidating
 * only `tasksKeys.list(vars.projectId)` + `tasksKeys.myInfinite()`
 * would leave the other-project list and the infinite variant stale.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { tasksKeys } from '../api';
import { useApplySmartTask } from '../smart-create-api';

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    POST: vi.fn(async () => ({
      data: { taskId: 'task-001', subtaskIds: ['sub-001', 'sub-002'] },
      error: undefined,
      response: new Response(null, { status: 200 }),
    })),
  },
  authSdk: {
    POST: vi.fn(async () => ({
      data: null,
      error: undefined,
      response: new Response(null, { status: 200 }),
    })),
  },
}));

function makeWrapper(qc: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe('useApplySmartTask invalidation', () => {
  it('invalidates every task-list query, in and out of the current project, but leaves detail queries alone', async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });

    const currentProjectList = tasksKeys.list('proj-A');
    const otherProjectList = tasksKeys.list('proj-B');
    const currentProjectInfinite = tasksKeys.infinite('proj-A');
    const myInfinite = tasksKeys.myInfinite();
    const unrelatedDetail = tasksKeys.detail('task-999');

    for (const key of [
      currentProjectList,
      otherProjectList,
      currentProjectInfinite,
      myInfinite,
      unrelatedDetail,
    ]) {
      qc.setQueryData(key, { seeded: true });
    }

    // Sanity check: nothing is invalidated before the mutation runs.
    for (const key of [
      currentProjectList,
      otherProjectList,
      currentProjectInfinite,
      myInfinite,
      unrelatedDetail,
    ]) {
      expect(qc.getQueryState(key)?.isInvalidated).toBe(false);
    }

    const { result } = renderHook(() => useApplySmartTask(), { wrapper: makeWrapper(qc) });

    await result.current.mutateAsync({
      workspaceId: 'ws-001',
      projectId: 'proj-A',
      title: 'Ship the release',
      description: '',
      priority: 2,
      assigneeUserIds: [],
      subtasks: [],
    });

    await waitFor(() => {
      expect(qc.getQueryState(currentProjectList)?.isInvalidated).toBe(true);
    });

    // The shared list prefix covers every list/infinite variant, for
    // the current project AND other projects.
    expect(qc.getQueryState(otherProjectList)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(currentProjectInfinite)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(myInfinite)?.isInvalidated).toBe(true);

    // A detail query outside the list prefix is untouched — the
    // invalidation is scoped, not a blanket `invalidateQueries()`.
    expect(qc.getQueryState(unrelatedDetail)?.isInvalidated).toBe(false);
  });
});
