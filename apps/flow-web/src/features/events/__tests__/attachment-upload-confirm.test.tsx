/**
 * Verify the post-upload size-enforcement handshake for event
 * attachments.
 *
 * Mirrors the task-side check: after a fresh blob is stored via the
 * presigned PUT, the client must call the calendar confirm endpoint so
 * the server can re-stat the object and reject an oversize upload
 * (deleting the attachment row). Invariants:
 *
 *   1. On the non-deduplicated branch, confirm is called with the
 *      attachment id from the presign response and the full calendar
 *      path (wsId / calId / evtId / attId).
 *   2. A confirm failure rejects the mutation and invalidates the
 *      event's attachment list so the deleted row disappears.
 *   3. The deduplicated short-circuit never calls confirm.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { eventAttachmentKeys, usePresignEventAttachmentMutation } from '../attachments-api';

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

const CONFIRM_PATH =
  '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}/confirm';
const PRESIGN_PATH = '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/presign';

const args = { wsId: 'ws-1', calId: 'cal-1', evtId: 'evt-1', file: new File(['x'], 'a.pdf') };

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

describe('usePresignEventAttachmentMutation confirm handshake', () => {
  it('calls confirm with the attachment id after a non-dedup PUT', async () => {
    postMock.mockImplementation(async (path: string) => {
      if (path === PRESIGN_PATH) {
        return {
          data: {
            storageKey: 'k',
            attachmentId: 'att-9',
            deduplicated: false,
            uploadUrl: 'https://blob.example/put',
          },
          error: undefined,
        };
      }
      return { data: { ok: true, byteSize: 1 }, error: undefined };
    });

    const qc = makeQc();
    const { result } = renderHook(() => usePresignEventAttachmentMutation(), {
      wrapper: makeWrapper(qc),
    });

    await result.current.mutateAsync(args);

    expect(fetch).toHaveBeenCalledTimes(1);
    expect(postMock).toHaveBeenCalledWith(CONFIRM_PATH, {
      params: { path: { wsId: 'ws-1', calId: 'cal-1', evtId: 'evt-1', attId: 'att-9' } },
    });
  });

  it('skips confirm on the deduplicated short-circuit', async () => {
    postMock.mockImplementation(async (path: string) => {
      if (path === PRESIGN_PATH) {
        return {
          data: { storageKey: 'k', attachmentId: 'att-dup', deduplicated: true },
          error: undefined,
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });

    const qc = makeQc();
    const { result } = renderHook(() => usePresignEventAttachmentMutation(), {
      wrapper: makeWrapper(qc),
    });

    await result.current.mutateAsync(args);

    expect(fetch).not.toHaveBeenCalled();
    expect(postMock).toHaveBeenCalledTimes(1);
    expect(postMock).not.toHaveBeenCalledWith(CONFIRM_PATH, expect.anything());
  });

  it('surfaces a confirm error and invalidates the attachments cache', async () => {
    postMock.mockImplementation(async (path: string) => {
      if (path === PRESIGN_PATH) {
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
      return {
        data: undefined,
        error: { title: 'Payload Too Large', status: 413, detail: 'file too big' },
      };
    });

    const qc = makeQc();
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => usePresignEventAttachmentMutation(), {
      wrapper: makeWrapper(qc),
    });

    await expect(result.current.mutateAsync(args)).rejects.toThrow();

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: eventAttachmentKeys.list('ws-1', 'cal-1', 'evt-1'),
      });
    });
  });
});
