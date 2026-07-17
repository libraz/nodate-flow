/**
 * Verify the post-upload size-enforcement handshake for task attachments.
 *
 * The presigned PUT binds only the SHA-256, not the length, so after a
 * fresh blob is stored the client must call
 * POST /tasks/{id}/attachments/{aid}/confirm. The server re-stats the
 * object and, on an oversize blob, deletes the attachment row and
 * returns an error. Two invariants matter:
 *
 *   1. On the non-deduplicated branch (a real PUT happened), confirm is
 *      called with the attachment id from the presign response.
 *   2. When confirm fails, the mutation rejects (so the UI can surface
 *      it) AND the attachments cache is invalidated (so the phantom,
 *      already-deleted row disappears).
 *
 * The deduplicated short-circuit path never stores a new blob, so it
 * must NOT call confirm.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { tasksKeys, usePresignUpload } from '../api';

const postMock = vi.fn();

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch exposes HTTP verbs uppercased.
    POST: (...args: unknown[]) => postMock(...args),
  },
}));

vi.mock('../../../lib/crypto/sha256', () => ({
  sha256Hex: vi.fn(async () => 'a'.repeat(64)),
}));

function makeWrapper(qc: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function makeQc(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

const file = new File(['payload'], 'photo.png', { type: 'image/png' });

beforeEach(() => {
  postMock.mockReset();
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(null, { status: 200 })),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('usePresignUpload confirm handshake', () => {
  it('calls confirm with the attachment id after a non-dedup PUT', async () => {
    postMock.mockImplementation(async (path: string) => {
      if (path === '/tasks/{id}/attachments/presign') {
        return {
          data: {
            storageKey: 'k',
            attachmentId: 'att-42',
            deduplicated: false,
            uploadUrl: 'https://blob.example/put',
          },
          error: undefined,
        };
      }
      // confirm
      return { data: { ok: true, byteSize: 7 }, error: undefined };
    });

    const qc = makeQc();
    const { result } = renderHook(() => usePresignUpload(), { wrapper: makeWrapper(qc) });

    await result.current.mutateAsync({ taskId: 'task-1', file });

    // The presigned PUT ran before confirm.
    expect(fetch).toHaveBeenCalledTimes(1);
    // Confirm was posted with the attachment id from the presign body.
    expect(postMock).toHaveBeenCalledWith('/tasks/{id}/attachments/{aid}/confirm', {
      params: { path: { id: 'task-1', aid: 'att-42' } },
    });
  });

  it('skips confirm on the deduplicated short-circuit', async () => {
    postMock.mockImplementation(async (path: string) => {
      if (path === '/tasks/{id}/attachments/presign') {
        return {
          data: { storageKey: 'k', attachmentId: 'att-dup', deduplicated: true },
          error: undefined,
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });

    const qc = makeQc();
    const { result } = renderHook(() => usePresignUpload(), { wrapper: makeWrapper(qc) });

    await result.current.mutateAsync({ taskId: 'task-1', file });

    expect(fetch).not.toHaveBeenCalled();
    expect(postMock).toHaveBeenCalledTimes(1);
    expect(postMock).not.toHaveBeenCalledWith(
      '/tasks/{id}/attachments/{aid}/confirm',
      expect.anything(),
    );
  });

  it('surfaces a confirm error and invalidates the attachments cache', async () => {
    postMock.mockImplementation(async (path: string) => {
      if (path === '/tasks/{id}/attachments/presign') {
        return {
          data: {
            storageKey: 'k',
            attachmentId: 'att-big',
            deduplicated: false,
            uploadUrl: 'https://blob.example/put',
          },
          error: undefined,
        };
      }
      // Oversize blob — server deleted the row and returned an error.
      return {
        data: undefined,
        error: { title: 'Payload Too Large', status: 413, detail: 'file too big' },
      };
    });

    const qc = makeQc();
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => usePresignUpload(), { wrapper: makeWrapper(qc) });

    await expect(result.current.mutateAsync({ taskId: 'task-1', file })).rejects.toThrow();

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: tasksKeys.attachments('task-1'),
      });
    });
  });
});
