/**
 * useCapabilities reports loading until the request settles, then either
 * the flags or a retry, and shares one request between its consumers.
 */

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: sdkMocks.get },
  authSdk: { GET: sdkMocks.get },
}));

import { resetCapabilitiesCache, useCapabilities } from '../use-capabilities';

const CAPS = {
  passwordLogin: false,
  oidcGoogle: true,
  oidcGithub: false,
  oidcMicrosoft: false,
  magicLink: false,
  totp: false,
  registrationOpen: true,
};

function ok(data: unknown): Promise<unknown> {
  return Promise.resolve({ data, error: null, response: new Response(null, { status: 200 }) });
}

function refused(): Promise<unknown> {
  return Promise.resolve({
    data: null,
    error: { detail: 'boom' },
    response: new Response(null, { status: 500 }),
  });
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  resetCapabilitiesCache();
});

describe('useCapabilities', () => {
  it('is loading, then ready with the server flags and no defaults', async () => {
    sdkMocks.get.mockReturnValue(ok(CAPS));
    const { result } = renderHook(() => useCapabilities());
    expect(result.current).toEqual({ status: 'loading' });
    await waitFor(() => {
      expect(result.current).toEqual({ status: 'ready', caps: CAPS });
    });
  });

  it('reports an error and recovers on retry', async () => {
    sdkMocks.get.mockReturnValueOnce(refused()).mockReturnValue(ok(CAPS));
    const { result } = renderHook(() => useCapabilities());
    await waitFor(() => {
      expect(result.current.status).toBe('error');
    });
    act(() => {
      if (result.current.status === 'error') result.current.retry();
    });
    expect(result.current).toEqual({ status: 'loading' });
    await waitFor(() => {
      expect(result.current).toEqual({ status: 'ready', caps: CAPS });
    });
  });

  it('shares one request between consumers mounted together', async () => {
    sdkMocks.get.mockReturnValue(ok(CAPS));
    const a = renderHook(() => useCapabilities());
    const b = renderHook(() => useCapabilities());
    await waitFor(() => {
      expect(a.result.current.status).toBe('ready');
      expect(b.result.current.status).toBe('ready');
    });
    expect(sdkMocks.get).toHaveBeenCalledTimes(1);
  });
});
