/**
 * The change-password section belongs to password accounts; an instance
 * without them must not show it.
 */

import { authStore } from '@nodate-flow/sdk';
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';

const sdkMocks = vi.hoisted(() => ({ get: vi.fn() }));
const capsMocks = vi.hoisted(() => ({
  state: { status: 'loading' } as
    | { status: 'loading' }
    | { status: 'error'; retry: () => void }
    | { status: 'ready'; caps: Record<string, boolean> },
}));

function ready(overrides: Record<string, boolean> = {}): typeof capsMocks.state {
  return {
    status: 'ready',
    caps: {
      passwordLogin: true,
      oidcGoogle: true,
      oidcGithub: false,
      oidcMicrosoft: false,
      magicLink: false,
      totp: true,
      registrationOpen: true,
      ...overrides,
    },
  };
}

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: sdkMocks.get },
  authSdk: { GET: sdkMocks.get },
}));

vi.mock('../../../features/auth/use-capabilities', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../features/auth/use-capabilities')>()),
  useCapabilities: () => capsMocks.state,
}));

import { SecurityPage } from '../security';

function setUser(hasPassword: boolean): void {
  authStore.getState().setSession('tok', {
    id: 'u1',
    email: 'a@b.test',
    displayName: 'Alice',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
    hasPassword,
  });
}

function mountSecurity(): void {
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
  const rootRoute = createRootRoute({ component: SecurityPage });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  });
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={instance}>{children}</I18nextProvider>;
  }
  render(<RouterProvider router={router} />, { wrapper: Wrapper });
}

beforeAll(() => {
  if (typeof window !== 'undefined' && typeof window.scrollTo !== 'function') {
    window.scrollTo = (() => {}) as typeof window.scrollTo;
  }
});

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.get.mockImplementation((path: string) =>
    Promise.resolve({
      data: path === '/me/sessions' ? { items: [] } : { status: 'disabled' },
      error: null,
      response: new Response(null, { status: 200 }),
    }),
  );
  setUser(true);
});

afterEach(() => {
  capsMocks.state = { status: 'loading' };
  authStore.getState().clearSession();
});

const passwordHeading = (): HTMLElement | null =>
  screen.queryByRole('heading', { name: enAuth.security.password_title });
const totpHeading = (): HTMLElement | null =>
  screen.queryByRole('heading', { name: enAuth.security.totp.title });

describe('SecurityPage and the capabilities', () => {
  it('shows neither section while the capabilities are pending', async () => {
    mountSecurity();
    await screen.findByRole('heading', { name: enAuth.security.title });
    expect(passwordHeading()).toBeNull();
    expect(totpHeading()).toBeNull();
  });

  it('offers a retry when the capabilities failed to load', async () => {
    const retry = vi.fn();
    capsMocks.state = { status: 'error', retry };
    mountSecurity();
    fireEvent.click(await screen.findByRole('button', { name: enAuth.fatal.retry }));
    expect(retry).toHaveBeenCalledTimes(1);
    expect(passwordHeading()).toBeNull();
    expect(totpHeading()).toBeNull();
  });

  it('shows password change and two-factor management when both are on', async () => {
    capsMocks.state = ready();
    mountSecurity();
    expect(
      await screen.findByRole('heading', { name: enAuth.security.password_title }),
    ).toBeDefined();
    expect(totpHeading()).not.toBeNull();
  });

  it('hides both when password login is disabled', async () => {
    capsMocks.state = ready({ passwordLogin: false, totp: false });
    mountSecurity();
    await screen.findByRole('heading', { name: enAuth.security.title });
    expect(passwordHeading()).toBeNull();
    expect(totpHeading()).toBeNull();
  });

  it('hides two-factor management when the server does not offer it', async () => {
    capsMocks.state = ready({ totp: false });
    mountSecurity();
    await screen.findByRole('heading', { name: enAuth.security.password_title });
    expect(totpHeading()).toBeNull();
  });

  it('hides both from an account without a local password', async () => {
    setUser(false);
    capsMocks.state = ready();
    mountSecurity();
    await screen.findByRole('heading', { name: enAuth.security.title });
    expect(passwordHeading()).toBeNull();
    expect(totpHeading()).toBeNull();
  });
});
