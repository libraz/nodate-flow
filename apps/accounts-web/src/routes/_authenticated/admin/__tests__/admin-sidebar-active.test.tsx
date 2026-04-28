/**
 * Verify the admin sidebar marks the current page as active.
 *
 * The admin shell renders six sidebar links. TanStack Router's `<Link>`
 * applies `activeProps.className` whenever the link's destination matches
 * the current URL, so the only thing this test pins is "the active class
 * is on the right link, and only that link, for at least two routes":
 *
 *   - /admin/users         -> only the "Users" link is active.
 *   - /admin/audit-logs    -> only the "Audit Logs" link is active.
 *
 * That covers the regression: previously every link had the same
 * `navLink` class with no active styling, so users had no visual
 * indicator of where they were.
 */

import { authStore } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import enAdmin from '../../../../../locales/en/admin.json';
import { AdminLayout } from '../../admin';

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
 * Build an in-memory router that renders `<AdminLayout>` at /admin and a
 * trivial child component at each leaf route. The leaf components don't
 * matter — we only assert on the sidebar links rendered by the layout.
 */
function mountAt(path: string): void {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const testI18n = buildI18n();

  const rootRoute = createRootRoute();
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: AdminLayout,
  });
  const childRoutes = ['users', 'workspaces', 'audit-logs', 'admins', 'stats', 'settings'].map(
    (segment) =>
      createRoute({
        getParentRoute: () => adminRoute,
        path: segment,
        component: () => <div data-testid={`page-${segment}`}>{segment}</div>,
      }),
  );

  const router = createRouter({
    routeTree: rootRoute.addChildren([adminRoute.addChildren(childRoutes)]),
    history: createMemoryHistory({ initialEntries: [path] }),
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
  authStore.getState().setSession('test-token', {
    id: 'admin-user',
    email: 'admin@test.local',
    displayName: 'Admin',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: true,
  });
});

afterEach(() => {
  authStore.getState().clearSession();
});

/**
 * The CSS-module class names get hashed at build time, but the production
 * source uses `styles.navLinkActive`. The only stable signal we can pin
 * here is "the link's className contains some token that includes
 * `navLinkActive`" — CSS Modules typically emit `_navLinkActive_xxx` or
 * `<file>_navLinkActive__xxx`. A regex tolerates both shapes.
 */
const activeClassRegex = /navLinkActive/;

describe('admin sidebar active state', () => {
  it('marks the Users link active on /admin/users', async () => {
    mountAt('/admin/users');

    await waitFor(() => {
      expect(screen.queryByTestId('page-users')).not.toBeNull();
    });

    const usersLink = screen.getByRole('link', { name: enAdmin.nav.users });
    const workspacesLink = screen.getByRole('link', { name: enAdmin.nav.workspaces });

    expect(usersLink.className).toMatch(activeClassRegex);
    expect(workspacesLink.className).not.toMatch(activeClassRegex);
  });

  it('marks the Audit Logs link active on /admin/audit-logs', async () => {
    mountAt('/admin/audit-logs');

    await waitFor(() => {
      expect(screen.queryByTestId('page-audit-logs')).not.toBeNull();
    });

    const auditLink = screen.getByRole('link', { name: enAdmin.nav.audit_logs });
    const usersLink = screen.getByRole('link', { name: enAdmin.nav.users });

    expect(auditLink.className).toMatch(activeClassRegex);
    expect(usersLink.className).not.toMatch(activeClassRegex);
  });
});
