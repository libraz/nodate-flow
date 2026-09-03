/**
 * Turning two-factor authentication on is password-gated at every step,
 * and the page has to say which of the two things went wrong: a mistyped
 * authenticator code or a mistyped account password. They call for
 * different corrections, and one generic line for both leaves the user
 * retyping the part that was already right.
 *
 * The password is asserted on the wire rather than in the source text,
 * so the check still holds when the call site is rewritten.
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
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
    DELETE: sdkMocks.delete,
  },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
  default: () => null,
  ToastProvider: () => null,
}));

vi.mock('qrcode', () => ({
  default: { toDataURL: () => Promise.resolve('data:image/png;base64,') },
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

function ok(data: unknown) {
  return Promise.resolve({ data, error: null, response: new Response(null, { status: 200 }) });
}

function refused(code: string, status = 400) {
  return Promise.resolve({
    data: null,
    error: { type: code, title: 'Bad Request', detail: 'refused', status },
    response: new Response(null, { status }),
  });
}

beforeAll(() => {
  if (typeof window !== 'undefined' && typeof window.scrollTo !== 'function') {
    window.scrollTo = (() => {}) as typeof window.scrollTo;
  }
});

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.delete.mockReset();
  sdkMocks.get.mockImplementation((path: string) => {
    if (path === '/me/totp') return ok({ status: 'disabled' });
    if (path === '/me/sessions') return ok({ items: [] });
    return ok({});
  });
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

async function startEnrollment(password: string): Promise<void> {
  mountSecurity();
  const start = await screen.findByRole('button', { name: enAuth.security.totp.start_setup });
  const passwordField = screen.getByLabelText(
    new RegExp(`^${enAuth.security.totp.password_required}`),
  );
  fireEvent.change(passwordField, { target: { value: password } });
  fireEvent.click(start);
}

describe('two-factor enrollment re-authentication', () => {
  it('carries the typed password on the enroll request', async () => {
    sdkMocks.post.mockImplementation(() =>
      ok({ secret: 'S3CR3T', otpauthUrl: 'otpauth://totp/x' }),
    );

    await startEnrollment('hunter2');

    await waitFor(() => {
      expect(sdkMocks.post).toHaveBeenCalledWith('/me/totp/enroll', {
        body: { password: 'hunter2' },
      });
    });
  });

  it('names the password, not the code, when the password is the part that was wrong', async () => {
    sdkMocks.post.mockImplementation(() => refused('AUTH.PASSWORD.CURRENT_MISMATCH', 401));

    await startEnrollment('wrong-password');

    // findByText throws when the message never renders.
    await screen.findByText(enAuth.security.totp.errors.password_mismatch);
    expect(screen.queryByText(enAuth.security.totp.errors.enroll_failed)).toBeNull();
  });

  it('falls back to the generic setup message for a refusal it cannot classify', async () => {
    sdkMocks.post.mockImplementation(() => refused('AUTH.TOTP.UNAVAILABLE', 503));

    await startEnrollment('hunter2');

    await screen.findByText(enAuth.security.totp.errors.enroll_failed);
  });

  it('reports a refusal that arrived with no error body at all', async () => {
    // A bodyless 405 or a gateway 502 reaches the client with no error
    // to read. Reporting it as a success would leave the account owner
    // believing two-factor setup had started.
    sdkMocks.post.mockImplementation(() =>
      Promise.resolve({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 502 }),
      }),
    );

    await startEnrollment('hunter2');

    await screen.findByText(enAuth.security.totp.errors.enroll_failed);
  });
});
