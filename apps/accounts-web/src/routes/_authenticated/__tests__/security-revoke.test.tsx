/**
 * Verify the active-sessions revoke flow surfaces failures via toast.
 *
 * Previously the handler used `.catch(() => {})` which silently swallowed
 * the error. The fix shows toast.error(t('security.session_revoke_failed'))
 * and clears the spinner so the user can retry.
 */

import { authStore } from '@nodate-flow/sdk';
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  delete: vi.fn(),
  post: vi.fn(),
}));

const toasterMock = vi.hoisted(() => ({
  show: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    DELETE: sdkMocks.delete,
    POST: sdkMocks.post,
  },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: toasterMock,
  default: () => null,
  ToastProvider: () => null,
}));

import { SecurityPage } from '../security';

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

function mountSecurity(): void {
  const testI18n = buildI18n();
  const rootRoute = createRootRoute({ component: SecurityPage });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>;
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
  sdkMocks.delete.mockReset();
  sdkMocks.post.mockReset();
  toasterMock.show.mockReset();
  authStore.getState().clearSession();
  authStore.getState().setSession('tok', {
    id: 'u1',
    email: 'a@b.test',
    displayName: 'Alice',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
  });
});

afterEach(() => {
  authStore.getState().clearSession();
});

describe('SecurityPage — revoke session error path', () => {
  it('shows a danger toast when revoke fails', async () => {
    // GET /me/sessions returns one revokable session.
    sdkMocks.get.mockImplementation((path: string) => {
      if (path === '/me/sessions') {
        return Promise.resolve({
          data: {
            items: [
              {
                id: 'sess-2',
                userAgent: 'Mozilla/5.0',
                ipAddress: '10.0.0.2',
                current: false,
                createdAt: 1_700_000_000,
                lastUsedAt: 1_700_000_100,
              },
            ],
          },
          error: null,
        });
      }
      // /me/totp etc — return resolvable empty.
      return Promise.resolve({ data: { status: 'disabled' }, error: null });
    });

    // DELETE rejects — this is the path that previously got swallowed.
    sdkMocks.delete.mockResolvedValue({ error: { detail: 'boom' }, data: null });

    mountSecurity();

    // Wait for the revoke button to appear.
    const revokeBtn = await screen.findByRole('button', {
      name: enAuth.security.session_revoke,
    });
    fireEvent.click(revokeBtn);

    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    const call = toasterMock.show.mock.calls[0]?.[0];
    expect(call?.tone).toBe('danger');
    expect(call?.message).toBe(enAuth.security.session_revoke_failed);
  });

  it('reports a refusal that arrived without an error body', async () => {
    sdkMocks.get.mockImplementation((path: string) => {
      if (path === '/me/sessions') {
        return Promise.resolve({
          data: {
            items: [
              {
                id: 'sess-2',
                userAgent: 'Mozilla/5.0',
                ipAddress: '10.0.0.2',
                current: false,
                createdAt: 1_700_000_000,
                lastUsedAt: 1_700_000_100,
              },
            ],
          },
          error: null,
        });
      }
      return Promise.resolve({ data: { status: 'disabled' }, error: null });
    });

    // A 403 with no body: openapi-fetch has nothing to parse, so it
    // leaves `error` undefined. The handler used to read that as
    // success and drop the session from the list while the server kept
    // it alive — the user is told a suspicious session is gone when it
    // is still able to act on their account.
    sdkMocks.delete.mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 403 }),
    });

    mountSecurity();

    const revokeBtn = await screen.findByRole('button', {
      name: enAuth.security.session_revoke,
    });
    fireEvent.click(revokeBtn);

    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    const call = toasterMock.show.mock.calls[0]?.[0];
    expect(call?.tone).toBe('danger');
    expect(call?.message).toBe(enAuth.security.session_revoke_failed);

    // And the row must still be there: the session was not revoked.
    expect(screen.getByRole('button', { name: enAuth.security.session_revoke })).toBeTruthy();
  });
});
