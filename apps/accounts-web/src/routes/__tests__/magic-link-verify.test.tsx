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
}));

vi.mock('../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
  },

  authSdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
  },
}));

import { Route as MagicLinkRoute } from '../magic-link';

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

function mountMagicLink(initialPath: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const testI18n = buildI18n();
  const magicLinkComponent = MagicLinkRoute.options.component;
  if (!magicLinkComponent) throw new Error('MagicLinkRoute is missing a component');
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const magicLinkRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/magic-link',
    validateSearch: MagicLinkRoute.options.validateSearch,
    component: magicLinkComponent,
  });
  const profileRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/profile',
    component: () => <div>profile</div>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([magicLinkRoute, profileRoute]),
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

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  authStore.getState().clearSession();
});

describe('magic-link verify route', () => {
  it('consumes the token, creates a session, and redirects to profile', async () => {
    sdkMocks.get.mockImplementation(async (path: string) => {
      if (path === '/auth/magic-link/verify') {
        return {
          data: { step: 'complete', accessToken: 'access-1' },
          error: null,
          response: new Response(null, { status: 200 }),
        };
      }
      if (path === '/me') {
        return {
          data: {
            id: 'user-1',
            email: 'user@example.test',
            displayName: 'User One',
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

    const router = mountMagicLink('/magic-link?token=plain-token-1');

    expect(await screen.findByText(enAuth.login.magic_link_verifying)).toBeDefined();
    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'));
    expect(authStore.getState().accessToken).toBe('access-1');
    expect(authStore.getState().user?.email).toBe('user@example.test');
    expect(sdkMocks.get).toHaveBeenCalledWith('/auth/magic-link/verify', {
      params: { query: { token: 'plain-token-1' } },
    });
  });

  it('finishes a TOTP-required magic-link login through /auth/login/totp', async () => {
    sdkMocks.get.mockImplementation(async (path: string) => {
      if (path === '/auth/magic-link/verify') {
        return {
          data: { step: 'totp_required', challengeToken: 'challenge-1' },
          error: null,
          response: new Response(null, { status: 200 }),
        };
      }
      if (path === '/me') {
        return {
          data: {
            id: 'user-2',
            email: 'totp@example.test',
            displayName: 'Totp User',
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
    sdkMocks.post.mockResolvedValueOnce({
      data: { accessToken: 'access-2' },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    const router = mountMagicLink('/magic-link?token=plain-token-2');

    const code = await screen.findByLabelText(new RegExp(`^${enAuth.login.totp_code}`));
    await userEvent.type(code, '123456');
    await userEvent.click(screen.getByRole('button', { name: enAuth.login.totp_submit }));

    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'));
    expect(sdkMocks.post).toHaveBeenCalledWith('/auth/login/totp', {
      body: { challengeToken: 'challenge-1', code: '123456' },
    });
    expect(authStore.getState().accessToken).toBe('access-2');
    expect(authStore.getState().user?.email).toBe('totp@example.test');
  });

  it('shows a malformed-link error when the token query param is missing', async () => {
    mountMagicLink('/magic-link');

    expect((await screen.findByRole('alert')).textContent).toBe(enAuth.errors.magic_link_malformed);
    expect(sdkMocks.get).not.toHaveBeenCalled();
  });
});
