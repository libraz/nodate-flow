/**
 * Verify that cancelling a pending TOTP enrollment does not lie about the
 * account state when the server rejects the request.
 *
 * DELETE /me/totp is password-gated and also clears a pending (un-confirmed)
 * secret. A previous implementation sent an empty password and ignored the
 * 4xx response, optimistically flipping the UI to "disabled" while the server
 * kept the pending secret. The cancel flow must instead surface the failure
 * via a danger toast and stay in its real (pending) state.
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
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    GET: sdkMocks.get,
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    DELETE: sdkMocks.delete,
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    POST: sdkMocks.post,
  },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: toasterMock,
  default: () => null,
  // biome-ignore lint/style/useNamingConvention: matches @nodate-flow/ui export name
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

describe('SecurityPage — cancel pending TOTP enrollment error path', () => {
  it('keeps the UI in its pending state and toasts when the cancel is rejected', async () => {
    // GET /me/totp reports a pending enrollment; /me/sessions returns empty.
    sdkMocks.get.mockImplementation((path: string) => {
      if (path === '/me/totp') {
        return Promise.resolve({ data: { status: 'pending' }, error: null });
      }
      if (path === '/me/sessions') {
        return Promise.resolve({ data: { items: [] }, error: null });
      }
      return Promise.resolve({ data: {}, error: null });
    });

    // DELETE /me/totp is rejected (e.g. password mismatch / still pending).
    sdkMocks.delete.mockResolvedValue({ error: { detail: 'boom' }, data: null });

    mountSecurity();

    // Resume view for a pending enrollment renders a "Restart setup" affordance.
    await screen.findByRole('button', { name: enAuth.security.totp.resume_restart });

    // Provide a non-empty password so the DELETE is actually attempted.
    // Anchor the label so it does not also match the "Current password" /
    // "New password" fields in the password-change section, and to tolerate
    // the required-field asterisk appended to the label text.
    const passwordField = screen.getByLabelText(
      new RegExp(`^${enAuth.security.totp.password_required}`),
    );
    fireEvent.change(passwordField, { target: { value: 'hunter2' } });

    fireEvent.click(screen.getByRole('button', { name: enAuth.security.totp.cancel }));

    // A danger toast surfaces the failure.
    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    const call = toasterMock.show.mock.calls[0]?.[0];
    expect(call?.tone).toBe('danger');
    expect(call?.message).toBe(enAuth.security.totp.errors.cancel_failed);

    // Crucially, the UI must NOT falsely show the disabled state.
    expect(screen.queryByText(enAuth.security.totp.disabled_description)).toBeNull();
    // It stays on the pending resume affordance.
    expect(
      screen.queryByRole('button', { name: enAuth.security.totp.resume_restart }),
    ).not.toBeNull();
  });
});
