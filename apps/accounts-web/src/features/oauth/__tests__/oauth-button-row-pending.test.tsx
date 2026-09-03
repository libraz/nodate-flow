/**
 * A12 — OAuthButtonRow disables every provider button while a `start`
 * call is in flight, and re-enables them when the call resolves with an
 * error so the user can retry. Success leaves the buttons disabled
 * because the browser is mid-`window.location` redirect and re-enabling
 * them would only re-open the multi-click race we are guarding against.
 */

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

const capsMocks = vi.hoisted(() => ({
  current: {
    passwordLogin: true,
    oidcGoogle: true,
    oidcGithub: true,
    oidcMicrosoft: false,
    magicLink: false,
    totp: false,
    registrationOpen: true,
  },
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
  },

  authSdk: {
    GET: sdkMocks.get,
  },
}));

vi.mock('../../auth/use-capabilities', () => ({
  useCapabilities: () => capsMocks.current,
}));

import OAuthButtonRow from '../oauth-button-row';

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

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  return <I18nextProvider i18n={buildI18n()}>{children}</I18nextProvider>;
}

beforeEach(() => {
  sdkMocks.get.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('<OAuthButtonRow> pending state (A12)', () => {
  it('disables every provider button while a start call is in flight', async () => {
    // Park the SDK call on a manual promise so the click handler has a
    // chance to flip the disabled flag before resolving.
    let resolveCall: (v: { data: { authorizationUrl: string }; error: null }) => void = () =>
      undefined;
    sdkMocks.get.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveCall = resolve;
        }),
    );

    render(<OAuthButtonRow mode="login" />, { wrapper: Wrapper });

    const google = screen.getByRole('button', { name: enAuth.login.sso_google });
    const github = screen.getByRole('button', { name: enAuth.login.sso_github });

    expect((google as HTMLButtonElement).disabled).toBe(false);
    expect((github as HTMLButtonElement).disabled).toBe(false);

    await userEvent.click(google);

    // After the first click, both buttons are disabled — only one start
    // call may be in flight at a time.
    expect((google as HTMLButtonElement).disabled).toBe(true);
    expect((github as HTMLButtonElement).disabled).toBe(true);
    expect(google.getAttribute('aria-busy')).toBe('true');
    expect(github.getAttribute('aria-busy')).toBe('false');

    // Resolve the promise so test cleanup is deterministic.
    resolveCall({ data: { authorizationUrl: 'about:blank' }, error: null });
  });

  it('re-enables the buttons when the start call returns an error', async () => {
    const onError = vi.fn();
    sdkMocks.get.mockResolvedValue({
      data: null,
      error: { type: 'AUTH.OIDC.UNAVAILABLE', detail: 'oops' },
      response: new Response(null, { status: 400 }),
    });

    render(<OAuthButtonRow mode="login" onError={onError} />, { wrapper: Wrapper });

    const google = screen.getByRole('button', { name: enAuth.login.sso_google });
    const github = screen.getByRole('button', { name: enAuth.login.sso_github });

    await userEvent.click(google);

    // Once the error path runs, the pending flag is cleared so the user
    // can retry on a different provider or the same one.
    expect(onError).toHaveBeenCalled();
    expect((google as HTMLButtonElement).disabled).toBe(false);
    expect((github as HTMLButtonElement).disabled).toBe(false);
  });

  it('a rapid second click during the pending window does not fire a second SDK call', async () => {
    let resolveCall: (v: { data: { authorizationUrl: string }; error: null }) => void = () =>
      undefined;
    sdkMocks.get.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveCall = resolve;
        }),
    );

    render(<OAuthButtonRow mode="login" />, { wrapper: Wrapper });

    const google = screen.getByRole('button', { name: enAuth.login.sso_google });
    const github = screen.getByRole('button', { name: enAuth.login.sso_github });

    await userEvent.click(google);
    // userEvent honours `disabled` so a second click is a no-op at the
    // DOM event layer; the JS handler also bails defensively on
    // pendingProvider !== null.
    await userEvent.click(github);
    await userEvent.click(google);

    expect(sdkMocks.get).toHaveBeenCalledTimes(1);
    expect(sdkMocks.get).toHaveBeenCalledWith('/auth/oidc/google/start');

    resolveCall({ data: { authorizationUrl: 'about:blank' }, error: null });
  });
});
