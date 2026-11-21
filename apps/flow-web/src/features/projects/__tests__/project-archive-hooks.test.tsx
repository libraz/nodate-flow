/**
 * Archive / unarchive hooks call the dedicated endpoints and refresh every
 * surface that lists projects, including the command palette's own cache.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: sdkMocks.post },
  authSdk: { POST: sdkMocks.post },
}));

import { projectsKeys, useArchiveProject, useUnarchiveProject } from '../api';

function setup(): { client: QueryClient; wrapper: (p: { children: ReactNode }) => ReactElement } {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false, throwOnError: false } },
  });
  return {
    client,
    wrapper: ({ children }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  };
}

beforeEach(() => {
  sdkMocks.post.mockReset().mockResolvedValue({
    data: { ok: true },
    error: null,
    response: new Response(null, { status: 200 }),
  });
});

describe.each([
  ['useArchiveProject', useArchiveProject, '/projects/{prjId}/archive'],
  ['useUnarchiveProject', useUnarchiveProject, '/projects/{prjId}/unarchive'],
] as const)('%s', (_name, useHook, path) => {
  it('posts to the endpoint and invalidates detail, lists and the palette projects', async () => {
    const { client, wrapper } = setup();
    const spy = vi.spyOn(client, 'invalidateQueries');
    const { result } = renderHook(() => useHook(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync('prj-1');
    });

    expect(sdkMocks.post).toHaveBeenCalledWith(path, { params: { path: { prjId: 'prj-1' } } });
    await waitFor(() => {
      const keys = spy.mock.calls.map((c) => c[0]?.queryKey);
      expect(keys).toContainEqual(projectsKeys.detail('prj-1'));
      expect(keys).toContainEqual([...projectsKeys.all, 'list']);
      expect(keys).toContainEqual(['command-palette', 'projects']);
    });
  });
});
