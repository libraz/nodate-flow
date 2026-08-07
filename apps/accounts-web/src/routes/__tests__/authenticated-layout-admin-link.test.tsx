/**
 * Verify the pathless /_authenticated layout renders the admin nav
 * link with a localized label (no hardcoded "Admin" string). Covers
 * en + ja to guard against the original i18n leak.
 */

import { type AuthUser, authStore } from '@nodate-flow/sdk';
import { render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../locales/en/admin.json';
import jaAdmin from '../../../locales/ja/admin.json';

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
  useLocation: () => ({ href: '/profile' }),
  Link: ({ children, to }: { children: ReactNode; to?: string }) => (
    <a href={to ?? '#'}>{children}</a>
  ),
  Outlet: () => null,
  createFileRoute: () => () => ({ options: {} }),
}));

// Stub useAuthBootstrap so we always read the store directly.
vi.mock('../../hooks/use-auth-bootstrap', () => ({
  useAuthBootstrap: () => ({ status: 'authenticated' }),
}));

const { AuthenticatedLayout } = await import('../_authenticated');

function buildI18n(lng: 'en' | 'ja'): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng,
      fallbackLng: 'en',
      defaultNS: 'admin',
      ns: ['admin'],
      resources: {
        en: { admin: enAdmin },
        ja: { admin: jaAdmin },
      },
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

function mountWithLng(lng: 'en' | 'ja'): void {
  const testI18n = buildI18n(lng);
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>;
  }
  render(<AuthenticatedLayout />, { wrapper: Wrapper });
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

describe('<AuthenticatedLayout> admin link i18n', () => {
  it('renders the localized admin label in English', () => {
    authStore.getState().setSession('tok', makeAdmin());
    mountWithLng('en');
    expect(screen.queryByText('Admin')).not.toBeNull();
    expect(screen.queryByText(enAdmin.title)).not.toBeNull();
  });

  it('renders the localized admin label in Japanese', () => {
    authStore.getState().setSession('tok', makeAdmin());
    mountWithLng('ja');
    expect(screen.queryByText(jaAdmin.title)).not.toBeNull();
    // The English literal must not appear when ja is active.
    expect(screen.queryByText('Admin')).toBeNull();
  });
});
