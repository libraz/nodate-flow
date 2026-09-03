/**
 * login.tsx TOTP / magic-link submit guards and recovery-code
 * input wiring.
 *
 *   - A fast double-Enter on the TOTP form must fire only ONE network
 *     request (synchronous re-entrancy guard via useSubmitGuard).
 *   - A fast double-Enter on the magic-link form must fire only ONE
 *     network request.
 *   - The recovery code field has `maxLength=20`, renders its helper
 *     text, and the helper is wired into `aria-describedby`.
 *   - The TOTP code field exposes a status string that reflects
 *     validation state ("6 digits required" while < 6 digits) and is
 *     wired through `aria-describedby` together with the error id when
 *     a server error is rendered.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router';
import { act, render, screen, waitFor } from '@testing-library/react';
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

const capsMocks = vi.hoisted(() => ({
  current: {
    passwordLogin: true,
    oidcGoogle: false,
    oidcGithub: false,
    oidcMicrosoft: false,
    magicLink: true,
    totp: true,
    registrationOpen: true,
  },
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

vi.mock('../../features/auth/use-capabilities', () => ({
  useCapabilities: () => capsMocks.current,
}));

import { Route as LoginRoute } from '../login';

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

function mountLogin(): void {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const testI18n = buildI18n();

  const loginComponent = LoginRoute.options.component;
  if (!loginComponent) throw new Error('LoginRoute is missing a component');
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/login',
    validateSearch: LoginRoute.options.validateSearch,
    component: loginComponent,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([loginRoute]),
    history: createMemoryHistory({ initialEntries: ['/login'] }),
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

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
});

/**
 * Drive the password form to the totp_required state so the TOTP
 * challenge form is rendered. The login POST resolves immediately with
 * a step=totp_required payload; the TOTP POST is not invoked yet.
 */
async function reachTotpStep(): Promise<void> {
  sdkMocks.post.mockImplementationOnce(async () => ({
    data: { step: 'totp_required', challengeToken: 'tok-1' },
    error: null,
    response: new Response(null, { status: 200 }),
  }));

  mountLogin();

  // Labels render with a trailing required `*` separated from the text
  // by the FormField's CSS, so we match by regex on the prefix.
  const email = await screen.findByLabelText(new RegExp(`^${enAuth.login.email}`));
  const password = await screen.findByLabelText(new RegExp(`^${enAuth.login.password}`));
  await userEvent.type(email, 'user@example.test');
  await userEvent.type(password, 'pw-strong-1');
  await userEvent.click(screen.getByRole('button', { name: enAuth.login.submit }));

  // Wait for the totp form to render.
  await screen.findByRole('heading', { name: enAuth.login.totp_title });
}

describe('login TOTP submit guard', () => {
  it('a fast double Enter on the TOTP form fires only ONE /auth/login/totp call', async () => {
    await reachTotpStep();

    // Park the totp POST so the second submit hits the in-flight guard.
    let resolveTotp: (v: unknown) => void = () => undefined;
    sdkMocks.post.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveTotp = resolve;
        }),
    );

    const codeInput = screen.getByLabelText(new RegExp(enAuth.login.totp_code, 'i'));
    await userEvent.type(codeInput, '123456');

    const submit = screen.getByRole('button', { name: enAuth.login.totp_submit });

    // Fire two synchronous clicks. The first triggers the network call,
    // the second must bail at the synchronous useSubmitGuard.guard()
    // ref-flag check before reaching the SDK. We use act() to make the
    // double dispatch land in the same React tick.
    await act(async () => {
      submit.click();
      submit.click();
    });

    // Exactly one in-flight totp request.
    const totpCalls = sdkMocks.post.mock.calls.filter((call) => call[0] === '/auth/login/totp');
    expect(totpCalls.length).toBe(1);

    // Resolve the parked promise so the test exits cleanly.
    resolveTotp({
      data: null,
      error: { type: 'auth.totp.code-mismatch' },
      response: new Response(null, { status: 400 }),
    });
  });
});

describe('login magic-link submit guard', () => {
  it('a fast double click on the magic-link form fires only ONE request', async () => {
    mountLogin();

    // Open the magic-link form.
    await userEvent.click(
      await screen.findByRole('button', { name: enAuth.login.magic_link_button }),
    );
    await screen.findByRole('heading', { name: enAuth.login.magic_link_title });

    // Park the magic-link POST so the second submit hits the guard.
    let resolveMagic: (v: unknown) => void = () => undefined;
    sdkMocks.post.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveMagic = resolve;
        }),
    );

    const email = screen.getByLabelText(new RegExp(`^${enAuth.login.email}`));
    await userEvent.type(email, 'user@example.test');

    const submit = screen.getByRole('button', { name: enAuth.login.magic_link_submit });
    await act(async () => {
      submit.click();
      submit.click();
    });

    const magicCalls = sdkMocks.post.mock.calls.filter(
      (call) => call[0] === '/auth/magic-link/request',
    );
    expect(magicCalls.length).toBe(1);

    resolveMagic({
      data: { ok: true },
      error: null,
      response: new Response(null, { status: 200 }),
    });
  });
});

describe('login recovery code field', () => {
  it('renders helper text, enforces maxLength=20, and wires aria-describedby', async () => {
    await reachTotpStep();

    // Toggle to recovery mode.
    await userEvent.click(screen.getByRole('button', { name: enAuth.login.totp_use_recovery }));

    const recoveryInput = screen.getByLabelText(
      new RegExp(enAuth.login.recovery_code, 'i'),
    ) as HTMLInputElement;
    expect(recoveryInput.maxLength).toBe(20);

    // Helper copy is rendered as the FormField description, which is
    // wired into the input's aria-describedby.
    const describedBy = recoveryInput.getAttribute('aria-describedby');
    expect(describedBy).not.toBeNull();
    const ids = (describedBy ?? '').split(' ').filter(Boolean);
    expect(ids.length).toBeGreaterThan(0);
    const helperEl = document.getElementById(ids[0] ?? '');
    expect(helperEl?.textContent).toBe(enAuth.login.recovery_helper);
  });
});

describe('login TOTP code aria wiring', () => {
  it('exposes a "needs digits" status when length < 6 and links it via aria-describedby', async () => {
    await reachTotpStep();

    const codeInput = screen.getByLabelText(
      new RegExp(enAuth.login.totp_code, 'i'),
    ) as HTMLInputElement;

    // Empty code → status reads "Enter all 6 digits".
    let describedBy = codeInput.getAttribute('aria-describedby');
    expect(describedBy).not.toBeNull();
    let firstHelperId = (describedBy ?? '').split(' ').filter(Boolean)[0] ?? '';
    expect(document.getElementById(firstHelperId)?.textContent).toBe(
      enAuth.login.totp_status_need_digits,
    );

    // Type 6 digits → status flips to the "awaiting verification" copy.
    await userEvent.type(codeInput, '123456');
    describedBy = codeInput.getAttribute('aria-describedby');
    firstHelperId = (describedBy ?? '').split(' ').filter(Boolean)[0] ?? '';
    expect(document.getElementById(firstHelperId)?.textContent).toBe(
      enAuth.login.totp_status_awaiting,
    );
  });

  it('appends the error id to aria-describedby when a server error renders', async () => {
    await reachTotpStep();

    sdkMocks.post.mockImplementationOnce(async () => ({
      data: null,
      error: { type: 'auth.totp.code-mismatch', status: 401, detail: 'no' },
      response: new Response(null, { status: 401 }),
    }));

    const codeInput = screen.getByLabelText(
      new RegExp(enAuth.login.totp_code, 'i'),
    ) as HTMLInputElement;
    await userEvent.type(codeInput, '123456');

    await userEvent.click(screen.getByRole('button', { name: enAuth.login.totp_submit }));

    // Wait for the error message to surface.
    await waitFor(() => {
      const ids = (codeInput.getAttribute('aria-describedby') ?? '').split(' ').filter(Boolean);
      // FormField always emits exactly one description id; with an error
      // present it appends a second errorId. Two ids means both status
      // and error were wired in for the screen reader.
      expect(ids.length).toBeGreaterThanOrEqual(2);
    });

    expect(codeInput.getAttribute('aria-invalid')).toBe('true');
  });
});
