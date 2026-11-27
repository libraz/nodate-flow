/**
 * Every mutation that can change what the AI status card reports
 * invalidates the status query for its workspace.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../../lib/api', () => ({
  apiRequest: () => Promise.resolve({}),
}));

import { useUpdateAutoActionSettings } from '../../workspaces/auto-action-settings-api';
import { aiStatusKeys } from '../ai-status-api';
import { useCreateAiProvider, useDeleteAiProvider, useUpdateAiProvider } from '../api';
import { useUpdateEmbeddingSettings } from '../embedding-settings-api';
import {
  useCreateProviderModel,
  usePatchProviderModel,
  useSetProviderDefaultModel,
} from '../models-api';

const WS = 'ws-1';

let qc: QueryClient;
let spy: ReturnType<typeof vi.spyOn>;

function wrapper({ children }: { children: ReactNode }): ReactNode {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function statusInvalidated(workspaceId: string): boolean {
  return spy.mock.calls.some(
    (call: readonly unknown[]) =>
      JSON.stringify((call[0] as { queryKey?: unknown }).queryKey) ===
      JSON.stringify(aiStatusKeys.detail(workspaceId)),
  );
}

beforeEach(() => {
  qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  spy = vi.spyOn(qc, 'invalidateQueries');
});

describe('AI status invalidation', () => {
  it('after creating a provider', async () => {
    const { result } = renderHook(() => useCreateAiProvider(WS), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({} as never);
    });
    expect(statusInvalidated(WS)).toBe(true);
    expect(statusInvalidated('ws-2')).toBe(false);
  });

  it('after deleting a provider', async () => {
    const { result } = renderHook(() => useDeleteAiProvider(WS), { wrapper });
    await act(async () => {
      await result.current.mutateAsync('p-1');
    });
    expect(statusInvalidated(WS)).toBe(true);
  });

  it('after rotating a provider key', async () => {
    const { result } = renderHook(() => useUpdateAiProvider(WS), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ providerId: 'p-1', patch: {} as never });
    });
    expect(statusInvalidated(WS)).toBe(true);
  });

  it.each([
    ['adding a model', () => useCreateProviderModel(WS, 'p-1'), {}],
    ['patching a model', () => usePatchProviderModel(WS, 'p-1'), { modelId: 'm-1', patch: {} }],
    ['setting the default model', () => useSetProviderDefaultModel(WS, 'p-1'), 'm-1'],
  ] as const)('after %s', async (_name, useHook, input) => {
    const { result } = renderHook(
      () => (useHook as () => { mutateAsync: (v: never) => Promise<unknown> })(),
      {
        wrapper,
      },
    );
    await act(async () => {
      await result.current.mutateAsync(input as never);
    });
    expect(statusInvalidated(WS)).toBe(true);
  });

  it('after changing the embedding policy', async () => {
    const { result } = renderHook(() => useUpdateEmbeddingSettings(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ workspaceId: WS, patch: {} as never });
    });
    expect(statusInvalidated(WS)).toBe(true);
  });

  it('after changing the auto-action settings', async () => {
    const { result } = renderHook(() => useUpdateAutoActionSettings(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ workspaceId: WS, patch: { enabled: false } });
    });
    expect(statusInvalidated(WS)).toBe(true);
  });
});
