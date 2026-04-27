/**
 * Verify the admin layout guard renders three states correctly:
 *   - user === null         → loading skeleton (Spinner), NOT null
 *   - user && !isInstanceAdmin → returns null (effect issues redirect)
 *   - user && isInstanceAdmin → renders the admin shell
 *
 * Previously the guard returned `null` while the auth bootstrap was in
 * flight, which caused a brief blank page on direct /admin loads.
 */

import { type AuthUser, authStore } from '@nodate-flow/sdk';
import { render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../../locales/en/admin.json';

// Stub TanStack Router primitives so we can render <AdminLayout> directly.
// The guard's interesting behavior is the three-way render branch, not the
// navigation side-effect — we only need to verify that useNavigate is *called*
// for the non-admin path (covered separately by Playwright).
const navigateMock = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
  // biome-ignore lint/style/useNamingConvention: matches @tanstack/react-router export name
  Link: ({ children }: { children: ReactNode }) => <span>{children}</span>,
  // biome-ignore lint/style/useNamingConvention: matches @tanstack/react-router export name
  Outlet: () => null,
  createFileRoute: () => () => ({ options: {} }),
}));

const { AdminLayout } = await import('../admin');

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

function makeAdmin(): AuthUser {
  return {
    id: 'admin-1',
    email: 'admin@example.test',
    displayName: 'Admin',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: true,
  };
}

function makeNonAdmin(): AuthUser {
  return { ...makeAdmin(), id: 'user-1', isInstanceAdmin: false };
}

function mountAdmin(): void {
  const testI18n = buildI18n();

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>;
  }
  render(<AdminLayout />, { wrapper: Wrapper });
}

beforeAll(() => {
  if (typeof window !== 'undefined' && typeof window.scrollTo !== 'function') {
    window.scrollTo = (() => {}) as typeof window.scrollTo;
  }
});

beforeEach(() => {
  navigateMock.mockReset();
  authStore.getState().clearSession();
});

afterEach(() => {
  authStore.getState().clearSession();
});

describe('<AdminLayout> guard', () => {
  it('renders a loading skeleton while user is null', () => {
    // No setSession call — user is null in the store.
    mountAdmin();
    expect(screen.queryByTestId('admin-guard-loading')).not.toBeNull();
    // The admin nav title must NOT yet be present.
    expect(screen.queryByText(enAdmin.title)).toBeNull();
    // Guard must NOT redirect while we're still loading.
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it('renders the admin shell when the user is an instance admin', () => {
    authStore.getState().setSession('tok', makeAdmin());
    mountAdmin();
    expect(screen.queryByTestId('admin-guard-loading')).toBeNull();
    expect(screen.queryByText(enAdmin.title)).not.toBeNull();
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it('returns null and triggers redirect for a non-admin user', () => {
    authStore.getState().setSession('tok', makeNonAdmin());
    mountAdmin();
    expect(screen.queryByTestId('admin-guard-loading')).toBeNull();
    expect(screen.queryByText(enAdmin.title)).toBeNull();
    expect(navigateMock).toHaveBeenCalledWith({ to: '/profile', replace: true });
  });
});
