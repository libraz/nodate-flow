/**
 * Verify the W6 first-run CTA on the profile page:
 *
 *   - When the authenticated user has no workspaces, a primary
 *     "Create workspace" CTA card renders at the top of the page.
 *   - When the user already has at least one workspace, the CTA is
 *     absent.
 *   - The page renders without crashing when `user.workspaces` is
 *     undefined and the workspaces fetch is still pending.
 */

import { type AuthUser, authStore } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
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
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  patch: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    PATCH: sdkMocks.patch,
  },
}));

vi.mock('../../../providers/theme-provider', () => ({
  useTheme: () => ({ setPreference: vi.fn() }),
}));

import { ProfilePage } from '../profile';

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

function makeUser(overrides: Partial<AuthUser> & { workspaces?: readonly unknown[] }): AuthUser & {
  workspaces?: readonly unknown[];
} {
  return {
    id: 'user-1',
    email: 'a@b.test',
    displayName: 'Tester',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
    ...overrides,
  };
}

function mountProfile(): void {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const testI18n = buildI18n();
  // ProfilePage uses <Link> from TanStack Router which crashes outside
  // a RouterProvider. Wrap it in a minimal in-memory router so the
  // component renders the same way it does in production.
  const rootRoute = createRootRoute({ component: ProfilePage });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
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

beforeAll(() => {
  if (typeof window !== 'undefined' && typeof window.scrollTo !== 'function') {
    window.scrollTo = (() => {}) as typeof window.scrollTo;
  }
});

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.patch.mockReset();
  authStore.getState().clearSession();
});

afterEach(() => {
  authStore.getState().clearSession();
});

describe('<ProfilePage> empty-workspaces CTA (W6)', () => {
  it('renders the CTA when the user has zero workspaces (inline)', async () => {
    authStore.getState().setSession('tok', makeUser({ workspaces: [] }));
    // Inline workspaces shortcut — the network call must not fire.
    mountProfile();

    const cta = await screen.findByTestId('empty-workspaces-cta');
    expect(cta).toBeDefined();
    expect(cta.textContent).toContain(enAuth.profile.empty_workspaces.title);
    expect(cta.textContent).toContain(enAuth.profile.empty_workspaces.body);
    expect(cta.textContent).toContain(enAuth.profile.empty_workspaces.cta);
    expect(sdkMocks.get).not.toHaveBeenCalled();
  });

  it('does not render the CTA when the user already has a workspace (inline)', () => {
    authStore.getState().setSession(
      'tok',
      makeUser({
        workspaces: [{ id: 'ws-1' }],
      }),
    );
    mountProfile();

    expect(screen.queryByTestId('empty-workspaces-cta')).toBeNull();
  });

  it('does not render the CTA when the workspaces fetch is still pending', async () => {
    // No inline workspaces; the hook will trigger /workspaces. Resolve
    // never — the CTA must stay hidden in the "unknown" state so we do
    // not flash the empty-state for users who actually have workspaces.
    authStore.getState().setSession('tok', makeUser({}));
    sdkMocks.get.mockReturnValue(new Promise(() => {}));

    mountProfile();

    // Page itself rendered fine — the title is visible.
    await screen.findByRole('heading', { name: enAuth.profile.title });
    expect(screen.queryByTestId('empty-workspaces-cta')).toBeNull();
  });

  it('renders the CTA when the workspaces fetch reports total === 0', async () => {
    authStore.getState().setSession('tok', makeUser({}));
    sdkMocks.get.mockResolvedValue({
      data: { workspaces: [], total: 0 },
      error: null,
    });

    mountProfile();

    await waitFor(() => {
      expect(screen.queryByTestId('empty-workspaces-cta')).not.toBeNull();
    });
  });
});
