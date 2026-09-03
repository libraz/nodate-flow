import { authStore } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../locales/en/auth.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  refresh: vi.fn(),
}));

vi.mock('../../lib/sdk', () => ({
  refreshAccessToken: sdkMocks.refresh,
  sdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
  },
  authSdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
  },
}));

import { rememberOidcRedirect, takeOidcRedirect } from '../../features/oauth/oidc-redirect';
import { Route as OIDCCompleteRoute } from '../oidc.complete';

/**
 * Replace the `href` setter so a completed sign-in reports where it would
 * navigate instead of actually leaving the page. `origin` is restated
 * explicitly: it lives on the prototype, so spreading `location` drops it
 * and the redirect check would have nothing to resolve against.
 */
function stubLocationHref(): { setHref: ReturnType<typeof vi.fn>; restore: () => void } {
  const original = window.location;
  const setHref = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      ...original,
      origin: original.origin,
      get href() {
        return original.href;
      },
      set href(value: string) {
        setHref(value);
      },
    },
  });
  return {
    setHref,
    restore: () => {
      Object.defineProperty(window, 'location', { configurable: true, value: original });
    },
  };
}

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

function mountOIDCComplete(initialPath: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const testI18n = buildI18n();
  const oidcComponent = OIDCCompleteRoute.options.component;
  if (!oidcComponent) throw new Error('OIDCCompleteRoute is missing a component');
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const oidcRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/oidc/complete',
    component: oidcComponent,
  });
  const profileRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/profile',
    component: () => <div>profile</div>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([oidcRoute, profileRoute]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }
  render(<RouterProvider router={router} />, { wrapper: Wrapper });
  return router;
}

function mockMe(email: string) {
  sdkMocks.get.mockImplementation(async (path: string) => {
    if (path === '/me') {
      return {
        data: {
          id: 'user-1',
          email,
          displayName: 'OIDC User',
          locale: 'en',
          timezone: 'UTC',
          country: 'US',
          themePreference: 'system',
          isInstanceAdmin: false,
          avatarUrl: null,
        },
        error: null,
        response: new Response(null, { status: 200 }),
      };
    }
    throw new Error(`unexpected GET ${path}`);
  });
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.refresh.mockReset();
  authStore.getState().clearSession();
  window.sessionStorage.clear();
});

describe('oidc complete route', () => {
  it('refreshes from the OIDC callback cookie and redirects to profile', async () => {
    sdkMocks.refresh.mockResolvedValueOnce('access-oidc-1');
    mockMe('oidc@example.test');

    const router = mountOIDCComplete('/oidc/complete#step=complete');

    expect(await screen.findByText(enAuth.login.oidc_verifying)).toBeDefined();
    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'));
    expect(sdkMocks.refresh).toHaveBeenCalledTimes(1);
    expect(authStore.getState().accessToken).toBe('access-oidc-1');
    expect(authStore.getState().user?.email).toBe('oidc@example.test');
  });

  it('lands on the page the sign-in started from', async () => {
    // A deep link into the product frontend: a different origin from this
    // app, allowed because it is on the configured redirect allowlist.
    const target = 'http://localhost:5173/workspaces/w1/tasks/t1';
    sdkMocks.refresh.mockResolvedValueOnce('access-oidc-3');
    mockMe('oidc-redirect@example.test');
    const location = stubLocationHref();
    try {
      rememberOidcRedirect(target);
      const router = mountOIDCComplete('/oidc/complete#step=complete');

      await waitFor(() => expect(location.setHref).toHaveBeenCalledWith(target));
      // The default landing page was not used.
      expect(router.state.location.pathname).toBe('/oidc/complete');
      // The target is consumed, so a later sign-in in the same tab starts clean.
      expect(takeOidcRedirect()).toBeNull();
    } finally {
      location.restore();
    }
  });

  it('refuses a redirect to an outside origin and falls back to profile', async () => {
    sdkMocks.refresh.mockResolvedValueOnce('access-oidc-4');
    mockMe('oidc-evil@example.test');
    const location = stubLocationHref();
    try {
      // Planted directly in storage: the entry is same-origin
      // script-writable, so the check on the way back has to be the one
      // that decides. `rememberOidcRedirect` would have refused this too.
      window.sessionStorage.setItem(
        'nf.oidc.redirect',
        JSON.stringify({ target: 'https://evil.example/steal', expiresAt: Date.now() + 60_000 }),
      );
      const router = mountOIDCComplete('/oidc/complete#step=complete');

      await waitFor(() => expect(router.state.location.pathname).toBe('/profile'));
      expect(location.setHref).not.toHaveBeenCalled();
    } finally {
      location.restore();
    }
  });

  it('names the provider flow rather than reusing the magic-link copy', async () => {
    sdkMocks.refresh.mockImplementation(() => new Promise(() => {}));

    mountOIDCComplete('/oidc/complete#step=complete');

    expect(await screen.findByText(enAuth.login.oidc_verifying)).toBeDefined();
    expect(screen.queryByText(enAuth.login.magic_link_verifying)).toBeNull();
    expect(screen.queryByText(enAuth.login.magic_link_title)).toBeNull();
  });

  it('finishes a TOTP-required OIDC callback through /auth/login/totp', async () => {
    mockMe('oidc-totp@example.test');
    sdkMocks.post.mockResolvedValueOnce({
      data: { accessToken: 'access-oidc-2' },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    const router = mountOIDCComplete(
      '/oidc/complete#step=totp_required&challengeToken=challenge-oidc',
    );

    const code = await screen.findByLabelText(new RegExp(`^${enAuth.login.totp_code}`));
    await userEvent.type(code, '123456');
    await userEvent.click(screen.getByRole('button', { name: enAuth.login.totp_submit }));

    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'));
    expect(sdkMocks.post).toHaveBeenCalledWith('/auth/login/totp', {
      body: { challengeToken: 'challenge-oidc', code: '123456' },
    });
    expect(sdkMocks.refresh).not.toHaveBeenCalled();
    expect(authStore.getState().accessToken).toBe('access-oidc-2');
    expect(authStore.getState().user?.email).toBe('oidc-totp@example.test');
  });
});
