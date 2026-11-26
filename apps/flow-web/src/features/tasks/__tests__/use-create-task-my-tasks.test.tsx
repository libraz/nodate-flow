/**
 * Creating a task, plainly or through AI assist, must mark both my-tasks
 * caches stale so Today, the home summary and the calendar pick the new task
 * up without waiting out their stale time.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: sdkMocks.post },
  authSdk: { POST: sdkMocks.post },
}));

import { calendarEventKeys } from '../../events/api';
import { myTasksKey, useCreateTask } from '../api';
import { useApplySmartTask } from '../smart-create-api';

const MY_TASKS_KEY = myTasksKey({ sort: 'dueOn' });
const CALENDAR_KEY = [...calendarEventKeys.meTasks, '2026-01-01', '2026-01-31'] as const;

function buildClient(): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  client.setQueryData(MY_TASKS_KEY, []);
  client.setQueryData(CALENDAR_KEY, { tasks: [] });
  return client;
}

function wrapperFor(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  sdkMocks.post.mockReset();
});

describe('my-tasks invalidation on create', () => {
  it('useCreateTask invalidates both my-tasks keys', async () => {
    sdkMocks.post.mockResolvedValue({
      data: { id: 'task-1', projectId: 'prj-1' },
      error: undefined,
      response: new Response(null, { status: 200 }),
    });
    const client = buildClient();
    const { result } = renderHook(() => useCreateTask(), { wrapper: wrapperFor(client) });

    await result.current.mutateAsync({
      projectId: 'prj-1',
      title: 'New task',
      priority: 2,
      visibility: 'public',
    });

    await waitFor(() => {
      expect(client.getQueryState(MY_TASKS_KEY)?.isInvalidated).toBe(true);
    });
    expect(client.getQueryState(CALENDAR_KEY)?.isInvalidated).toBe(true);
  });

  it('useApplySmartTask invalidates both my-tasks keys', async () => {
    sdkMocks.post.mockResolvedValue({
      data: { taskId: 'task-1', subtaskIds: [] },
      error: undefined,
      response: new Response(null, { status: 200 }),
    });
    const client = buildClient();
    const { result } = renderHook(() => useApplySmartTask(), { wrapper: wrapperFor(client) });

    await result.current.mutateAsync({
      workspaceId: 'ws-1',
      projectId: 'prj-1',
      title: 'Ship it',
      description: '',
      priority: 2,
      assigneeUserIds: ['user-self'],
      subtasks: [],
    });

    await waitFor(() => {
      expect(client.getQueryState(MY_TASKS_KEY)?.isInvalidated).toBe(true);
    });
    expect(client.getQueryState(CALENDAR_KEY)?.isInvalidated).toBe(true);
  });
});
