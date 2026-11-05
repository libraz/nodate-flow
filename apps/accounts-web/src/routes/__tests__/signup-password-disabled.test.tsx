/**
 * /signup creates a password account, so an instance that has none sends
 * the visitor to /login instead of showing the form.
 */

import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../locales/en/auth.json';

const capsMocks = vi.hoisted(() => ({
  state: { status: 'loading' } as
    | { status: 'loading' }
    | { status: 'error'; retry: () => void }
    | { status: 'ready'; caps: { passwordLogin: boolean; registrationOpen: boolean } },
}));

vi.mock('../../lib/sdk', () => ({
  sdk: { GET: vi.fn(), POST: vi.fn() },
  authSdk: { GET: vi.fn(), POST: vi.fn() },
}));

vi.mock('../../features/auth/use-capabilities', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../features/auth/use-capabilities')>()),
  useCapabilities: () => capsMocks.state,
}));

import { Route as SignupRoute } from '../signup';

function mountSignup(): { pathname: () => string } {
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

  const signupComponent = SignupRoute.options.component;
  if (!signupComponent) throw new Error('SignupRoute is missing a component');
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const signupRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/signup',
    component: signupComponent,
  });
  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/login',
    component: () => <p>login page</p>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([signupRoute, loginRoute]),
    history: createMemoryHistory({ initialEntries: ['/signup'] }),
  });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={instance}>{children}</I18nextProvider>;
  }
  render(<RouterProvider router={router} />, { wrapper: Wrapper });
  return { pathname: () => router.state.location.pathname };
}

afterEach(() => {
  capsMocks.state = { status: 'loading' };
});

describe('<SignupPage> and the capabilities', () => {
  it('renders nothing while the capabilities are pending', () => {
    const view = mountSignup();
    expect(screen.queryByRole('button')).toBeNull();
    expect(view.pathname()).toBe('/signup');
  });

  it('offers a retry when the capabilities failed to load', async () => {
    const retry = vi.fn();
    capsMocks.state = { status: 'error', retry };
    mountSignup();
    fireEvent.click(await screen.findByRole('button', { name: enAuth.fatal.retry }));
    expect(retry).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: enAuth.signup.submit })).toBeNull();
  });

  it('redirects to /login when password login is disabled', async () => {
    capsMocks.state = {
      status: 'ready',
      caps: { passwordLogin: false, registrationOpen: true },
    };
    const view = mountSignup();
    await screen.findByText('login page');
    await waitFor(() => {
      expect(view.pathname()).toBe('/login');
    });
    expect(screen.queryByRole('button', { name: enAuth.signup.submit })).toBeNull();
  });

  it('shows the sign-up form when password login is enabled', async () => {
    capsMocks.state = {
      status: 'ready',
      caps: { passwordLogin: true, registrationOpen: true },
    };
    mountSignup();
    expect(await screen.findByRole('button', { name: enAuth.signup.submit })).toBeDefined();
  });
});
