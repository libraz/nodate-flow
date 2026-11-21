/** Remaining recovery-code count when two-factor is on (`GET /me/totp/recovery-codes`). */

import { authStore } from '@nodate-flow/sdk';
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
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

describe('SecurityPage -- recovery code count', () => {
  it('shows the remaining count when two-factor is enabled', async () => {
    sdkMocks.get.mockImplementation((path: string) => {
      if (path === '/me/totp') return ok({ status: 'enabled' });
      if (path === '/me/totp/recovery-codes') return ok({ remaining: 3 });
      return ok({ items: [] });
    });

    mountSecurity();

    expect(await screen.findByText('3 recovery codes remaining')).toBeTruthy();
    expect(sdkMocks.get).toHaveBeenCalledWith('/me/totp/recovery-codes');
  });

  it('does not ask for the count while two-factor is off', async () => {
    sdkMocks.get.mockImplementation((path: string) =>
      path === '/me/totp' ? ok({ status: 'disabled' }) : ok({ items: [] }),
    );

    mountSecurity();

    await screen.findByRole('button', { name: 'Start setup' });
    expect(sdkMocks.get).not.toHaveBeenCalledWith('/me/totp/recovery-codes');
    expect(screen.queryByTestId('recovery-remaining')).toBeNull();
  });

  it('leaves the count out when it cannot be read', async () => {
    sdkMocks.get.mockImplementation((path: string) => {
      if (path === '/me/totp') return ok({ status: 'enabled' });
      if (path === '/me/totp/recovery-codes') {
        return Promise.resolve({
          data: undefined,
          error: undefined,
          response: new Response(null, { status: 500 }),
        });
      }
      return ok({ items: [] });
    });

    mountSecurity();

    await waitFor(() => {
      expect(sdkMocks.get).toHaveBeenCalledWith('/me/totp/recovery-codes');
    });
    expect(screen.queryByTestId('recovery-remaining')).toBeNull();
  });
});
