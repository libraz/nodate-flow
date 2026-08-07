/**
 * An expired session must not cost the user their destination: the
 * /_authenticated guard hands /login the page that was asked for, the
 * same way the product frontend's guard does. Without it, a bookmark or
 * an emailed link to /security or /workspaces/... silently became a trip
 * to /profile.
 */

import { authStore } from '@nodate-flow/sdk';
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router';
import { render, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../locales/en/admin.json';

vi.mock('../../hooks/use-auth-bootstrap', () => ({
  useAuthBootstrap: () => ({ status: 'unauthenticated' }),
}));

const { AuthenticatedLayout } = await import('../_authenticated');

interface LoginSearch {
  redirect?: string;
}

function buildI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'admin',
      ns: ['admin'],
      resources: { en: { admin: enAdmin } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return instance;
}

/**
 * Minimal stand-in for the real route tree: the pathless guard wrapping a
 * protected page, plus a /login that validates `redirect` exactly as the
 * real one does.
 */
function mountGuardedAt(initialPath: string) {
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const layoutRoute = createRoute({
    getParentRoute: () => rootRoute,
    id: '_authenticated',
    component: AuthenticatedLayout,
  });
  const securityRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: '/security',
    component: () => <div>security</div>,
  });
  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/login',
    validateSearch: (search: Record<string, unknown>): LoginSearch => {
      const redirect = typeof search.redirect === 'string' ? search.redirect : undefined;
      return redirect ? { redirect } : {};
    },
    component: () => <div>login</div>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([layoutRoute.addChildren([securityRoute]), loginRoute]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });

  const testI18n = buildI18n();
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>;
  }
  render(<RouterProvider router={router} />, { wrapper: Wrapper });
  return router;
}

beforeEach(() => {
  authStore.getState().clearSession();
});

describe('<AuthenticatedLayout> unauthenticated redirect', () => {
  it('sends the requested page along to /login', async () => {
    const router = mountGuardedAt('/security?foo=1');

    await waitFor(() => expect(router.state.location.pathname).toBe('/login'));
    const search = router.state.location.search as LoginSearch;
    expect(search.redirect).toBe('/security?foo=1');
    // The query string has to survive serialization too — /login reads it
    // back off the URL after a full page load.
    expect(router.state.location.searchStr).toContain(encodeURIComponent('/security?foo=1'));
  });
});
