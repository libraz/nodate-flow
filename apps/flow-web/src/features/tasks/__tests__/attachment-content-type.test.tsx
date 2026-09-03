/**
 * What content type an attachment upload declares when the browser has
 * none to offer.
 *
 * `File.type` is empty for anything the browser's table does not cover —
 * server logs, database dumps, extension-less exports. Declaring the
 * generic binary type for all of them throws away the filename, which is
 * the only remaining hint, so the upload falls back to the extension
 * first. The value asserted here is also the one the presigned PUT is
 * signed with, so the metadata and the PUT header have to agree.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { usePresignUpload } from '../api';

const postMock = vi.fn();

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    POST: (...args: unknown[]) => postMock(...args),
  },

  authSdk: {
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

interface PresignCall {
  body: { contentType: string };
}

/** Runs the upload and returns the contentType it declared at presign. */
async function declaredContentType(file: File): Promise<{ presign: string; put: string }> {
  postMock.mockImplementation(async (path: string) => {
    if (path === '/tasks/{id}/attachments/presign') {
      return {
        data: {
          storageKey: 'k',
          attachmentId: 'att-1',
          deduplicated: false,
          uploadUrl: 'https://blob.example/put',
        },
        error: undefined,
        response: new Response(null, { status: 200 }),
      };
    }
    return {
      data: { ok: true, byteSize: file.size },
      error: undefined,
      response: new Response(null, { status: 200 }),
    };
  });

  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const { result } = renderHook(() => usePresignUpload(), { wrapper: makeWrapper(qc) });
  await result.current.mutateAsync({ taskId: 'task-1', file });

  const presignCall = postMock.mock.calls.find(
    (call) => call[0] === '/tasks/{id}/attachments/presign',
  );
  if (!presignCall) throw new Error('presign was never called');
  const putCall = vi.mocked(fetch).mock.calls[0];
  const putHeaders = (putCall?.[1]?.headers ?? {}) as Record<string, string>;
  return {
    presign: (presignCall[1] as PresignCall).body.contentType,
    put: putHeaders['Content-Type'] ?? '',
  };
}

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

describe('attachment upload content type', () => {
  it('names the type from the extension when the browser reports none', async () => {
    const { presign, put } = await declaredContentType(new File(['x'], 'app.log', { type: '' }));
    expect(presign).toBe('text/plain');
    expect(put).toBe('text/plain');
  });

  it('reports a file nothing identifies as generic binary', async () => {
    // The server accepts this as unidentified rather than rejecting it,
    // which is the whole point: there is no other way to attach it.
    const { presign } = await declaredContentType(new File(['x'], 'release-notes', { type: '' }));
    expect(presign).toBe('application/octet-stream');
  });

  it("keeps the browser's own verdict when it has one", async () => {
    const { presign } = await declaredContentType(
      new File(['x'], 'photo.png', { type: 'image/png' }),
    );
    expect(presign).toBe('image/png');
  });
});
