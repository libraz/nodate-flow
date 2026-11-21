/** Remaining recovery-code count when two-factor is on (`GET /me/totp/recovery-codes`). */

import { authStore } from '@nodate-flow/sdk';
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';

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

function withI18n(children: ReactNode): ReactElement {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'auth',
      ns: ['auth'],
      resources: { en: { auth: enAuth } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return <I18nextProvider i18n={instance}>{children}</I18nextProvider>;
}

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
