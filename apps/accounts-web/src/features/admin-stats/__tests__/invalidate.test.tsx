/**
 * Verify that useInvalidateInstanceStats invalidates the canonical
 * `adminStatsKeys.all` query, so admin grant/revoke and user
 * suspend/enable handlers can call it without coupling to the underlying
 * key shape.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { adminStatsKeys, useInvalidateInstanceStats } from '../api';

function makeWrapper(qc: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe('useInvalidateInstanceStats', () => {
  it('invalidates the admin-stats query key', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');
    const wrapper = makeWrapper(qc);

    const { result } = renderHook(() => useInvalidateInstanceStats(), { wrapper });
    await result.current();

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: adminStatsKeys.all });
  });
});
