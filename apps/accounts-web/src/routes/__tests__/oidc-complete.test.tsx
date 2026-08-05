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
}));

import { Route as OIDCCompleteRoute } from '../oidc.complete';

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
});

describe('oidc complete route', () => {
  it('refreshes from the OIDC callback cookie and redirects to profile', async () => {
    sdkMocks.refresh.mockResolvedValueOnce('access-oidc-1');
    mockMe('oidc@example.test');

    const router = mountOIDCComplete('/oidc/complete#step=complete');

    expect(await screen.findByText(enAuth.login.magic_link_verifying)).toBeDefined();
    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'));
    expect(sdkMocks.refresh).toHaveBeenCalledTimes(1);
    expect(authStore.getState().accessToken).toBe('access-oidc-1');
    expect(authStore.getState().user?.email).toBe('oidc@example.test');
  });

  it('finishes a TOTP-required OIDC callback through /auth/login/totp', async () => {
    mockMe('oidc-totp@example.test');
    sdkMocks.post.mockResolvedValueOnce({ data: { accessToken: 'access-oidc-2' }, error: null });

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
