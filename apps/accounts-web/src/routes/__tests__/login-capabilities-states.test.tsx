/**
 * The sections of /login that depend on the capabilities render only once
 * they are known: nothing while the request is pending, a retry when it
 * fails.
 */

import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router';
import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../locales/en/auth.json';

const capsMocks = vi.hoisted(() => ({
  state: { status: 'loading' } as
    | { status: 'loading' }
    | { status: 'error'; retry: () => void }
    | { status: 'ready'; caps: Record<string, boolean> },
}));

vi.mock('../../lib/sdk', () => ({
  sdk: { GET: vi.fn(), POST: vi.fn() },
  authSdk: { GET: vi.fn(), POST: vi.fn() },
}));

vi.mock('../../features/auth/use-capabilities', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../features/auth/use-capabilities')>()),
  useCapabilities: () => capsMocks.state,
}));

import { Route as LoginRoute } from '../login';

function mountLogin(): void {
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
  const component = LoginRoute.options.component;
  if (!component) throw new Error('LoginRoute is missing a component');
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/login',
    validateSearch: LoginRoute.options.validateSearch,
    component,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([loginRoute]),
    history: createMemoryHistory({ initialEntries: ['/login'] }),
  });
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={instance}>{children}</I18nextProvider>;
  }
  render(<RouterProvider router={router} />, { wrapper: Wrapper });
}

beforeEach(() => {
  capsMocks.state = { status: 'loading' };
});

describe('<LoginPage> while the capabilities are unknown', () => {
  it('shows only the title while the request is pending', async () => {
    mountLogin();
    expect(await screen.findByRole('heading', { name: enAuth.login.title })).toBeDefined();
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.queryByText(enAuth.login.signup_link)).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('offers a retry when the request failed, and no form', async () => {
    const retry = vi.fn();
    capsMocks.state = { status: 'error', retry };
    mountLogin();
    expect(await screen.findByText(enAuth.fatal.title)).toBeDefined();
    expect(screen.queryByRole('button', { name: enAuth.login.submit })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: enAuth.fatal.retry }));
    expect(retry).toHaveBeenCalledTimes(1);
  });
});
