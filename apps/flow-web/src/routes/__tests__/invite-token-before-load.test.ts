/**
 * /invite/$token route — verifies `beforeLoad` prefetches the invite-info
 * query and *does not* re-throw on failure, so the lazy component can
 * render its branded error state instead of the generic route fallback.
 */

import { QueryClient } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock('../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
  },
  authSdk: {
    GET: sdkMocks.get,
  },
}));

import { Route } from '../invite.$token';

beforeEach(() => {
  sdkMocks.get.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

interface BeforeLoadArgs {
  params: { token: string };
  context: { queryClient: QueryClient };
}

function callBeforeLoad(token: string, qc: QueryClient): Promise<unknown> {
  // The `beforeLoad` option is normally invoked by the router with a
  // narrow argument shape. We replicate the relevant subset here so we
  // can drive it directly without a router instance.
  const beforeLoad = Route.options.beforeLoad as (args: BeforeLoadArgs) => Promise<unknown>;
  return beforeLoad({ params: { token }, context: { queryClient: qc } });
}

describe('/invite/$token beforeLoad prefetch (B / item 36)', () => {
  it('prefetches the invite-info query into the cache on success', async () => {
    sdkMocks.get.mockResolvedValueOnce({
      data: { workspaceName: 'Acme', role: 'member' },
      error: null,
    });

    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    await callBeforeLoad('tok-1', qc);

    // After beforeLoad resolves the cache has the prefetched payload so
    // the lazy component's useSuspenseQuery resolves synchronously.
    const cached = qc.getQueryData(['invites', 'info', 'tok-1']);
    expect(cached).toEqual({ workspaceName: 'Acme', role: 'member' });
    expect(sdkMocks.get).toHaveBeenCalledWith('/invites/{token}/info', {
      params: { path: { token: 'tok-1' } },
    });
  });

  it('does not re-throw when the prefetch fails (lets the component render its branded error)', async () => {
    sdkMocks.get.mockResolvedValueOnce({
      data: null,
      error: { type: 'INVITE.TOKEN.INVALID', detail: 'gone' },
    });

    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    // The contract is that beforeLoad resolves (does not throw) so the
    // route mounts the lazy component, which surfaces the branded
    // suspense-error UI keyed off the same query.
    await expect(callBeforeLoad('tok-bad', qc)).resolves.toBeUndefined();
  });
});
