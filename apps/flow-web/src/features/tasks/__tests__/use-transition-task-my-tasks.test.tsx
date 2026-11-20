/**
 * useTransitionTask and the "my tasks" caches: `['me','tasks']` is updated
 * optimistically and rolled back on failure, and both my-tasks keys are
 * refetched once the last in-flight transition settles.
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
import { type MyTaskListItem, type TaskDerivedState, useTransitionTask } from '../api';

const CALENDAR_KEY = [...calendarEventKeys.meTasks, '2026-01-01', '2026-01-31'] as const;

function myRow(id: string, derivedState: TaskDerivedState): MyTaskListItem {
  return {
    id,
    title: `Task ${id}`,
    derivedState,
    priority: 0,
    projectId: 'prj-1',
    workspaceId: 'ws-1',
    workspaceName: 'Workspace',
    actorRole: 'assignee',
    createdAt: 1,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function okBody(id: string, derivedState: TaskDerivedState) {
  return {
    data: { id, projectId: 'prj-1', derivedState },
    error: undefined,
    response: new Response(null, { status: 200 }),
  };
}

function buildClient(): QueryClient {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  client.setQueryData(calendarEventKeys.myTasks, [myRow('a', 'open'), myRow('b', 'open')]);
  client.setQueryData(CALENDAR_KEY, { tasks: [] });
  return client;
}

function wrapperFor(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

function stateOf(client: QueryClient, id: string): string | undefined {
  return client
    .getQueryData<MyTaskListItem[]>(calendarEventKeys.myTasks)
    ?.find((row) => row.id === id)?.derivedState;
}

function invalidated(client: QueryClient): {
  mine: boolean | undefined;
  calendar: boolean | undefined;
} {
  return {
    mine: client.getQueryState(calendarEventKeys.myTasks)?.isInvalidated,
    calendar: client.getQueryState(CALENDAR_KEY)?.isInvalidated,
  };
}

beforeEach(() => {
  sdkMocks.post.mockReset();
});

describe('useTransitionTask — my tasks cache', () => {
  it('flips the row optimistically while the request is in flight', async () => {
    const gate = deferred<unknown>();
    sdkMocks.post.mockImplementation(() => gate.promise);
    const client = buildClient();
    const { result } = renderHook(() => useTransitionTask(), { wrapper: wrapperFor(client) });

    const pending = result.current.mutateAsync({
      id: 'a',
      transition: 'complete',
      projectId: 'prj-1',
      optimisticState: 'done',
    });
    await waitFor(() => {
      expect(stateOf(client, 'a')).toBe('done');
    });
    expect(stateOf(client, 'b')).toBe('open');

    gate.resolve(okBody('a', 'done'));
    await pending;
  });

  it('rolls the row back when the request fails', async () => {
    sdkMocks.post.mockImplementation(() =>
      Promise.resolve({
        data: undefined,
        error: { type: 'about:blank', title: 'Internal Server Error', status: 500 },
        response: new Response(null, { status: 400 }),
      }),
    );
    const client = buildClient();
    const { result } = renderHook(() => useTransitionTask(), { wrapper: wrapperFor(client) });

    await expect(
      result.current.mutateAsync({
        id: 'a',
        transition: 'complete',
        projectId: 'prj-1',
        optimisticState: 'done',
      }),
    ).rejects.toBeDefined();

    expect(stateOf(client, 'a')).toBe('open');
  });

  it('leaves the rows alone without an optimistic state', async () => {
    const gate = deferred<unknown>();
    sdkMocks.post.mockImplementation(() => gate.promise);
    const client = buildClient();
    const { result } = renderHook(() => useTransitionTask(), { wrapper: wrapperFor(client) });

    const pending = result.current.mutateAsync({ id: 'a', transition: 'complete' });
    await waitFor(() => {
      expect(sdkMocks.post).toHaveBeenCalledTimes(1);
    });
    expect(stateOf(client, 'a')).toBe('open');

    gate.resolve(okBody('a', 'done'));
    await pending;
  });

  it('invalidates both my-tasks keys after a transition settles', async () => {
    sdkMocks.post.mockImplementation(() => Promise.resolve(okBody('a', 'done')));
    const client = buildClient();
    expect(invalidated(client)).toEqual({ mine: false, calendar: false });
    const { result } = renderHook(() => useTransitionTask(), { wrapper: wrapperFor(client) });

    await result.current.mutateAsync({
      id: 'a',
      transition: 'complete',
      projectId: 'prj-1',
      optimisticState: 'done',
    });

    await waitFor(() => {
      expect(invalidated(client)).toEqual({ mine: true, calendar: true });
    });
  });

  it('invalidates both keys after a failed transition too', async () => {
    sdkMocks.post.mockImplementation(() =>
      Promise.resolve({
        data: undefined,
        error: { type: 'about:blank', title: 'Internal Server Error', status: 500 },
        response: new Response(null, { status: 400 }),
      }),
    );
    const client = buildClient();
    const { result } = renderHook(() => useTransitionTask(), { wrapper: wrapperFor(client) });

    await expect(
      result.current.mutateAsync({ id: 'a', transition: 'complete', projectId: 'prj-1' }),
    ).rejects.toBeDefined();

    await waitFor(() => {
      expect(invalidated(client)).toEqual({ mine: true, calendar: true });
    });
  });

  it('does not refetch while another transition is still in flight', async () => {
    const first = deferred<unknown>();
    const second = deferred<unknown>();
    sdkMocks.post
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);
    const client = buildClient();
    const { result } = renderHook(() => useTransitionTask(), { wrapper: wrapperFor(client) });

    const a = result.current.mutateAsync({
      id: 'a',
      transition: 'complete',
      projectId: 'prj-1',
      optimisticState: 'done',
    });
    const b = result.current.mutateAsync({
      id: 'b',
      transition: 'complete',
      projectId: 'prj-1',
      optimisticState: 'done',
    });
    await waitFor(() => {
      expect(sdkMocks.post).toHaveBeenCalledTimes(2);
    });

    first.resolve(okBody('a', 'done'));
    await a;
    expect(invalidated(client)).toEqual({ mine: false, calendar: false });

    second.resolve(okBody('b', 'done'));
    await b;
    await waitFor(() => {
      expect(invalidated(client)).toEqual({ mine: true, calendar: true });
    });
  });
});
