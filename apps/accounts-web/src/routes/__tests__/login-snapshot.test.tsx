/**
 * Snapshot guard for the W7 OAuth row extraction. Mounts the login page
 * with a representative caps payload (all OIDC providers enabled, plus
 * magic link) and asserts the rendered DOM still contains the same
 * provider buttons + divider that lived inline before the refactor.
 *
 * This is a behavioural snapshot, not a string snapshot, so the test
 * stays meaningful even when unrelated styling changes ship.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../locales/en/auth.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}));

const capsMocks = vi.hoisted(() => ({
  current: {
    passwordLogin: true,
    oidcGoogle: true,
    oidcGithub: true,
    oidcMicrosoft: true,
    magicLink: true,
    totp: false,
    registrationOpen: true,
  },
}));

vi.mock('../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    GET: sdkMocks.get,
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    POST: sdkMocks.post,
  },
}));

vi.mock('../../features/auth/use-capabilities', () => ({
  useCapabilities: () => capsMocks.current,
}));

import { Route as LoginRoute } from '../login';

function buildI18n(): ReturnType<typeof i18n.createInstance> {
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
  return instance;
}

function mountLogin(): void {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const testI18n = buildI18n();

  // Build an in-memory router with a real `/login` child route so the
  // page's `useSearch({ from: '/login' })` call resolves. We re-use
  // the production route's `validateSearch` + `component` exactly so
  // the snapshot exercises the same code path.
  // The route's `component` is typed as possibly undefined upstream.
  // We narrow with a runtime assertion because production builds
  // always provide one and the test would be meaningless without it.
  const loginComponent = LoginRoute.options.component;
  if (!loginComponent) throw new Error('LoginRoute is missing a component');
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/login',
    validateSearch: LoginRoute.options.validateSearch,
    component: loginComponent,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([loginRoute]),
    history: createMemoryHistory({ initialEntries: ['/login'] }),
  });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }
  render(<RouterProvider router={router} />, { wrapper: Wrapper });
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
});

describe('<LoginPage> after OAuthButtonRow extraction (W7)', () => {
  it('still renders the email + password form', async () => {
    mountLogin();
    expect(await screen.findByRole('heading', { name: enAuth.login.title })).toBeDefined();
    expect(screen.getByRole('button', { name: enAuth.login.submit })).toBeDefined();
  });

  it('still renders one button per enabled OIDC provider', async () => {
    mountLogin();
    expect(await screen.findByRole('button', { name: enAuth.login.sso_google })).toBeDefined();
    expect(screen.getByRole('button', { name: enAuth.login.sso_github })).toBeDefined();
    expect(screen.getByRole('button', { name: enAuth.login.sso_microsoft })).toBeDefined();
  });

  it('still renders the SSO divider copy from the login namespace', async () => {
    mountLogin();
    await screen.findByText(enAuth.login.sso_divider);
  });
});
