/**
 * Session bootstrap when the network drops during load.
 *
 * A refresh that never reached the server and a refresh the server
 * refused both produce "no token", and the bootstrap used to treat them
 * the same: clear the session and bounce to login. For someone holding a
 * perfectly valid refresh cookie whose connection blinked while the app
 * was loading, that means being signed out — and losing whatever was on
 * screen — with no way back but a manual reload.
 *
 * The module memoizes its probe, so each test re-imports it through
 * `vi.resetModules()` to get a fresh one.
 */

import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  refresh: vi.fn(),
  lastFailure: vi.fn(),
  meGet: vi.fn(),
  clearSession: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => {
  const refreshAccessToken = Object.assign(mocks.refresh, { lastFailure: mocks.lastFailure });
  return {
    refreshAccessToken,
    // openapi-fetch keys its client by HTTP method, hence the caps.
    authSdk: { GET: mocks.meGet },
    // lib/api builds a requester per client, so both have to exist even
    // though this probe only reaches auth-api.
    sdk: { GET: vi.fn() },
  };
});

vi.mock('../../../providers/query-client', () => ({
  queryClient: { setQueryData: vi.fn() },
}));

vi.mock('../../../i18n', () => ({
  i18n: { language: 'en' },
  setLanguage: vi.fn(),
  supportedLanguages: ['en', 'ja', 'zh'],
}));

vi.mock('../auth-store', () => ({
  authStore: {
    getState: () => ({
      clearSession: mocks.clearSession,
      setSession: vi.fn(),
    }),
  },
}));

type BootstrapModule = typeof import('../use-auth-bootstrap');

async function freshModule(): Promise<BootstrapModule> {
  vi.resetModules();
  return import('../use-auth-bootstrap');
}

const PROFILE = {
  id: 'u1',
  email: 'a@example.com',
  displayName: 'A',
  locale: 'en',
  timezone: 'UTC',
  country: 'JP',
  themePreference: 'system',
  isInstanceAdmin: false,
};

beforeEach(() => {
  mocks.refresh.mockReset();
  mocks.lastFailure.mockReset().mockReturnValue('none');
  mocks.meGet.mockReset();
  mocks.clearSession.mockReset();
});

describe('useAuthBootstrap', () => {
  it('reports offline, and leaves the session alone, when the probe could not reach the server', async () => {
    mocks.refresh.mockResolvedValue(null);
    mocks.lastFailure.mockReturnValue('network');
    const { useAuthBootstrap } = await freshModule();

    const { result } = renderHook(() => useAuthBootstrap());

    await waitFor(() => {
      expect(result.current.status).toBe('offline');
    });
    expect(mocks.clearSession).not.toHaveBeenCalled();
  });

  it('reports unauthenticated when the server refused the refresh', async () => {
    mocks.refresh.mockResolvedValue(null);
    mocks.lastFailure.mockReturnValue('rejected');
    const { useAuthBootstrap } = await freshModule();

    const { result } = renderHook(() => useAuthBootstrap());

    await waitFor(() => {
      expect(result.current.status).toBe('unauthenticated');
    });
  });

  it('retries against the network instead of replaying the cached failure', async () => {
    // The offline outcome must not be memoized, or the retry button would
    // resolve from cache and never touch the network again.
    mocks.refresh.mockResolvedValueOnce(null);
    mocks.lastFailure.mockReturnValueOnce('network');
    const { useAuthBootstrap } = await freshModule();

    const { result } = renderHook(() => useAuthBootstrap());
    await waitFor(() => {
      expect(result.current.status).toBe('offline');
    });

    mocks.refresh.mockResolvedValue('token-abc');
    mocks.lastFailure.mockReturnValue('none');
    mocks.meGet.mockResolvedValue({ data: PROFILE, error: null, response: { ok: true } });

    result.current.retry();

    await waitFor(() => {
      expect(result.current.status).toBe('authenticated');
    });
    expect(mocks.refresh).toHaveBeenCalledTimes(2);
  });

  it('keeps the freshly minted session when the profile probe drops mid-flight', async () => {
    mocks.refresh.mockResolvedValue('token-abc');
    mocks.meGet.mockRejectedValue(new TypeError('Failed to fetch'));
    const { useAuthBootstrap } = await freshModule();

    const { result } = renderHook(() => useAuthBootstrap());

    await waitFor(() => {
      expect(result.current.status).toBe('offline');
    });
    expect(mocks.clearSession).not.toHaveBeenCalled();
  });

  it('clears the session when the profile probe is refused', async () => {
    mocks.refresh.mockResolvedValue('token-abc');
    mocks.meGet.mockResolvedValue({
      data: null,
      error: { type: 'AUTH.TOKEN.INVALID' },
      response: { ok: false },
    });
    const { useAuthBootstrap } = await freshModule();

    const { result } = renderHook(() => useAuthBootstrap());

    await waitFor(() => {
      expect(result.current.status).toBe('unauthenticated');
    });
    expect(mocks.clearSession).toHaveBeenCalled();
  });
});
