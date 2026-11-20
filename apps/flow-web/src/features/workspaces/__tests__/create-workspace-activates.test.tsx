/**
 * Creating a workspace must leave it active by the time the mutation
 * resolves, with the membership cache refreshed first — otherwise the
 * current-workspace hook still sees the old id set and drops the new one.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/api')>('../../../lib/api');
  return {
    ...actual,
    authApiRequest: async () => ({ id: 'ws-new', name: 'New', slug: 'new' }),
  };
});

import { useCreateWorkspace } from '../api';

beforeEach(() => {
  window.localStorage.clear();
});

describe('useCreateWorkspace', () => {
  it('invalidates member-ids and activates the new workspace before resolving', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    client.setQueryData(['workspaces', 'member-ids'], ['ws-old']);
    const wrapper = ({ children }: { children: ReactNode }): ReactElement => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useCreateWorkspace(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ name: 'New', slug: 'new' });
    });

    expect(window.localStorage.getItem('nf.activeWsId')).toBe('ws-new');
    expect(client.getQueryState(['workspaces', 'member-ids'])?.isInvalidated).toBe(true);
  });
});
