import { toaster } from '@nodate-flow/ui/primitives/toast';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { authStore } from '../../auth/auth-store';
import { setStreamHealthy, useStreamHealthy } from '../stream-health';
import { useWorkspaceStream } from '../use-workspace-stream';

function wrapper({ children }: { children: ReactNode }): ReactElement {
  const client = new QueryClient();
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe('useWorkspaceStream polling fallback', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    authStore.getState().setAccessToken('test-token');
    setStreamHealthy(true);
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
  });

  afterEach(() => {
    authStore.getState().clearSession();
    setStreamHealthy(true);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('enables polling after 90 seconds of immediate reconnect failures', async () => {
    const { result: health } = renderHook(() => useStreamHealthy());
    const { unmount } = renderHook(() => useWorkspaceStream('ws-1'), { wrapper });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(90_000);
    });

    expect(health.current).toBe(false);
    expect(fetch).toHaveBeenCalled();
    unmount();
  });
  it('refreshes silently on first subscription and notifies a later resync', async () => {
    const toast = vi.spyOn(toaster, 'show').mockReturnValue('test-toast');
    const client = new QueryClient();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    let controller!: ReadableStreamDefaultController<Uint8Array>;
    const stream = new ReadableStream<Uint8Array>({
      start(value) {
        controller = value;
      },
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
    const testWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { unmount } = renderHook(() => useWorkspaceStream('ws-1'), { wrapper: testWrapper });
    const resync = new TextEncoder().encode(
      'data: {"kind":"resync","workspaceId":"ws-1","at":1}\n\n',
    );
    await act(async () => {
      controller.enqueue(resync);
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(toast).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(750);
    });
    expect(invalidate).toHaveBeenCalled();
    await act(async () => {
      controller.enqueue(resync);
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(toast).toHaveBeenCalledTimes(1);
    controller.close();
    unmount();
  });

  it('marks stale while hidden and refetches active stale queries once on show', async () => {
    const client = new QueryClient();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const refetch = vi.spyOn(client, 'refetchQueries').mockResolvedValue();
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    let controller!: ReadableStreamDefaultController<Uint8Array>;
    const stream = new ReadableStream<Uint8Array>({
      start(value) {
        controller = value;
      },
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
    const testWrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { unmount } = renderHook(() => useWorkspaceStream('ws-1'), { wrapper: testWrapper });
    await act(async () => {
      controller.enqueue(
        new TextEncoder().encode('data: {"kind":"resync","workspaceId":"ws-1","at":1}\n\n'),
      );
      await vi.advanceTimersByTimeAsync(750);
    });
    expect(invalidate).toHaveBeenCalledWith(expect.objectContaining({ refetchType: 'none' }));
    expect(refetch).not.toHaveBeenCalled();
    hidden.mockReturnValue(false);
    document.dispatchEvent(new Event('visibilitychange'));
    document.dispatchEvent(new Event('visibilitychange'));
    expect(refetch).toHaveBeenCalledTimes(1);
    expect(refetch).toHaveBeenCalledWith({ type: 'active', stale: true });
    controller.close();
    unmount();
  });
});
