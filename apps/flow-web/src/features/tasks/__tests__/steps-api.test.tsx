/**
 * Unit tests for the step-decomposition hooks. These exercise the
 * UI-only `uiId` augmentation that wraps each `/propose-steps`
 * response so list rows can use a stable React key.
 *
 * Two invariants matter:
 *   1. Within a single propose call, every step gets a unique uiId.
 *   2. Re-proposing yields a fresh batch of uiIds — they must not
 *      collide with the previous batch (otherwise stale local state
 *      from the first batch could be re-bound to a different logical
 *      step in the second batch).
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { useProposeSteps } from '../steps-api';

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    POST: vi.fn(async () => ({
      data: {
        parentTaskId: 'task-001',
        steps: [
          { title: 'Alpha', description: 'first', priority: 'low' },
          { title: 'Beta', description: 'second', priority: 'medium' },
          { title: 'Gamma', description: 'third', priority: 'high' },
        ],
      } as unknown,
      error: undefined,
    })),
  },
}));

function makeWrapper(qc: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe('useProposeSteps uiId augmentation', () => {
  it('assigns a unique uiId to every step in a single response', async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const { result } = renderHook(() => useProposeSteps(), { wrapper: makeWrapper(qc) });

    const out = await result.current.mutateAsync({ taskId: 'task-001' });

    await waitFor(() => {
      expect(out.steps).toHaveLength(3);
    });

    const ids = out.steps.map((s) => s.uiId);
    // Every uiId is a non-empty string.
    for (const id of ids) {
      expect(typeof id).toBe('string');
      expect(id.length).toBeGreaterThan(0);
    }
    // No collisions inside the batch.
    expect(new Set(ids).size).toBe(ids.length);
    // Original payload fields are preserved.
    expect(out.steps.map((s) => s.title)).toEqual(['Alpha', 'Beta', 'Gamma']);
  });

  it('produces a disjoint set of uiIds when re-proposing', async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const { result } = renderHook(() => useProposeSteps(), { wrapper: makeWrapper(qc) });

    const first = await result.current.mutateAsync({ taskId: 'task-001' });
    const second = await result.current.mutateAsync({ taskId: 'task-001' });

    const firstIds = new Set(first.steps.map((s) => s.uiId));
    const secondIds = new Set(second.steps.map((s) => s.uiId));

    // No id from the first batch may appear in the second — otherwise
    // stale local UI state could leak across re-proposes.
    for (const id of secondIds) {
      expect(firstIds.has(id)).toBe(false);
    }
  });
});
