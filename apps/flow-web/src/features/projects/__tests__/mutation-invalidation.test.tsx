/**
 * Verify the W5 invalidation policy for the projects feature: mutation
 * hooks must broadcast `invalidateQueries` against the documented
 * scopes (detail + scoped list), not the over-broad `projectsKeys.all`.
 *
 * The test wires {@link useUpdateProject} into a real
 * QueryClient + Provider tree, mocks the SDK so the network call
 * resolves synchronously with a fake project, then asserts the spy
 * recorded invalidations for the correct keys.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({
  patch: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    PATCH: sdkMocks.patch,
  },
}));

import { projectsKeys, useUpdateProject } from '../api';

function buildClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
}

function wrapperFactory(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  sdkMocks.patch.mockReset();
});

describe('useUpdateProject — W5 invalidation matrix', () => {
  it('invalidates the project detail key + the scoped list prefix only', async () => {
    sdkMocks.patch.mockResolvedValue({
      data: {
        id: 'prj-1',
        workspaceId: 'ws-1',
        name: 'Renamed',
        slug: 'renamed',
        identifier: 'PRJ',
        isArchived: false,
        featureCalendar: false,
        featureLenses: false,
        featurePages: false,
        featureTimeboxes: false,
        createdAt: 1,
      },
      error: null,
    });

    const client = buildClient();
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(() => useUpdateProject(), {
      wrapper: wrapperFactory(client),
    });

    await act(async () => {
      await result.current.mutateAsync({ id: 'prj-1', patch: { name: 'Renamed' } });
    });

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalled();
    });

    const calledKeys = invalidateSpy.mock.calls.map((c) => c[0]?.queryKey);
    // Detail key for the affected project must be invalidated.
    expect(calledKeys).toContainEqual(projectsKeys.detail('prj-1'));
    // List prefix must be invalidated (not bare `projectsKeys.all`).
    expect(calledKeys).toContainEqual([...projectsKeys.all, 'list']);
    // The bare `projectsKeys.all` must NOT be sent — that would
    // collaterally nuke members / dependencies sub-queries.
    for (const key of calledKeys) {
      if (Array.isArray(key) && key.length === 1 && key[0] === 'projects') {
        throw new Error(
          `useUpdateProject must not invalidate the bare projectsKeys.all (got ${JSON.stringify(key)})`,
        );
      }
    }
  });
});
