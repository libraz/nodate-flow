/** "Sign out of all other devices" on the security page (`DELETE /me/sessions`). */

import { authStore } from '@nodate-flow/sdk';
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({ get: vi.fn(), delete: vi.fn(), post: vi.fn() }));
const toasterMock = vi.hoisted(() => ({ show: vi.fn() }));
const confirmMock = vi.hoisted(() => ({ confirmAction: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: sdkMocks.get, DELETE: sdkMocks.delete, POST: sdkMocks.post },
  authSdk: { GET: sdkMocks.get, DELETE: sdkMocks.delete, POST: sdkMocks.post },
}));
vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: toasterMock,
  default: () => null,
  ToastProvider: () => null,
}));
vi.mock('@nodate-flow/ui/primitives/confirm/action', () => confirmMock);
vi.mock('../../../features/auth/use-capabilities', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../features/auth/use-capabilities')>()),
  useCapabilities: () => ({
    status: 'ready',
    caps: {
      passwordLogin: true,
      oidcGoogle: false,
      oidcGithub: false,
      oidcMicrosoft: false,
      magicLink: false,
      totp: true,
      registrationOpen: true,
    },
  }),
}));

import { newKeys } from '../../../features/profile/__tests__/new-auth-keys';
import { withI18n } from '../../../features/profile/__tests__/test-i18n';
import { SecurityPage } from '../security';

function mountSecurity(): void {
  const rootRoute = createRootRoute({ component: () => withI18n(<SecurityPage />) });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  });
  render(<RouterProvider router={router} />);
}

function ok(data: unknown) {
  return Promise.resolve({ data, error: null, response: new Response(null, { status: 200 }) });
}

function session(id: string, current: boolean) {
  return {
    id,
    userAgent: 'Mozilla/5.0',
    ipAddress: '10.0.0.2',
    current,
    createdAt: 1_700_000_000,
    lastUsedAt: 1_700_000_100,
  };
}

beforeAll(() => {
  if (typeof window.scrollTo !== 'function') window.scrollTo = (() => {}) as typeof window.scrollTo;
});

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.delete.mockReset();
  toasterMock.show.mockReset();
  confirmMock.confirmAction.mockReset();
  authStore.getState().clearSession();
  authStore.getState().setSession('tok', {
    id: 'u1',
    email: 'a@b.test',
    displayName: 'Alice',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
    hasPassword: true,
  });
});

describe('SecurityPage -- sign out of all other devices', () => {
  it('asks first, revokes, then refetches the session list', async () => {
    let listCalls = 0;
    sdkMocks.get.mockImplementation((path: string) => {
      if (path === '/me/sessions') {
        listCalls += 1;
        return ok({
          items:
            listCalls === 1 ? [session('s1', true), session('s2', false)] : [session('s1', true)],
        });
      }
      return ok({ status: 'disabled' });
    });
    sdkMocks.delete.mockImplementation(() => ok({ ok: true, revoked: 1 }));
    confirmMock.confirmAction.mockResolvedValueOnce(false).mockResolvedValueOnce(true);

    mountSecurity();
    const button = await screen.findByRole('button', { name: newKeys.security.session_revoke_all });

    fireEvent.click(button);
    await waitFor(() => {
      expect(confirmMock.confirmAction).toHaveBeenCalledTimes(1);
    });
    expect(sdkMocks.delete).not.toHaveBeenCalled();

    fireEvent.click(button);
    await waitFor(() => {
      expect(sdkMocks.delete).toHaveBeenCalledWith('/me/sessions');
    });
    await waitFor(() => {
      expect(listCalls).toBe(2);
    });
    expect(toasterMock.show).toHaveBeenCalledWith(
      expect.objectContaining({ tone: 'success', message: 'Revoked 1 other session(s)' }),
    );
    // Only the current session is left, so the action is no longer offered.
    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: newKeys.security.session_revoke_all }),
      ).toBeNull();
    });
  });

  it('reports a refusal and does not refetch', async () => {
    let listCalls = 0;
    sdkMocks.get.mockImplementation((path: string) => {
      if (path === '/me/sessions') {
        listCalls += 1;
        return ok({ items: [session('s1', true), session('s2', false)] });
      }
      return ok({ status: 'disabled' });
    });
    sdkMocks.delete.mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 500 }),
    });
    confirmMock.confirmAction.mockResolvedValue(true);

    mountSecurity();
    fireEvent.click(
      await screen.findByRole('button', { name: newKeys.security.session_revoke_all }),
    );

    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    const call = toasterMock.show.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(call.tone).toBe('danger');
    expect(call.message.startsWith(newKeys.security.session_revoke_all_failed)).toBe(true);
    expect(listCalls).toBe(1);
  });

  it('is not offered when this is the only session', async () => {
    sdkMocks.get.mockImplementation((path: string) =>
      path === '/me/sessions' ? ok({ items: [session('s1', true)] }) : ok({ status: 'disabled' }),
    );
    mountSecurity();
    await screen.findByText(/10\.0\.0\.2/);
    expect(screen.queryByRole('button', { name: newKeys.security.session_revoke_all })).toBeNull();
  });
});
