/**
 * Verify the W7 shared OAuth button row component:
 *
 *   - Renders one button per enabled OIDC provider in `caps`.
 *   - Renders nothing while caps are loading or when no provider is
 *     enabled, so callers can drop the row in unconditionally.
 *   - Surfaces error keys to the parent via the `onError` callback.
 *   - Exposes the `mode` prop on the rendered DOM root so login and
 *     signup can each emit a stable selector for analytics / E2E.
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
  current: null as null | {
    passwordLogin: boolean;
    oidcGoogle: boolean;
    oidcGithub: boolean;
    oidcMicrosoft: boolean;
    magicLink: boolean;
    totp: boolean;
    registrationOpen: boolean;
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
import { takeOidcRedirect } from '../oidc-redirect';

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
  capsMocks.current = null;
  window.sessionStorage.clear();
});

/** Caps with Google enabled — the shape most of these cases need. */
function googleOnlyCaps(): NonNullable<typeof capsMocks.current> {
  return {
    passwordLogin: true,
    oidcGoogle: true,
    oidcGithub: false,
    oidcMicrosoft: false,
    magicLink: false,
    totp: false,
    registrationOpen: true,
  };
}

/**
 * Replace the `href` setter so the provider hand-off is observable
 * without leaving the page. `origin` is restated explicitly: it lives on
 * the prototype, so spreading `location` drops it and the redirect check
 * would have nothing to resolve against.
 */
function stubLocationHref(): { setHref: ReturnType<typeof vi.fn>; restore: () => void } {
  const original = window.location;
  const setHref = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      ...original,
      origin: original.origin,
      get href() {
        return original.href;
      },
      set href(value: string) {
        setHref(value);
      },
    },
  });
  return {
    setHref,
    restore: () => {
      Object.defineProperty(window, 'location', { configurable: true, value: original });
    },
  };
}

afterEach(() => {
  capsMocks.current = null;
});

describe('<OAuthButtonRow>', () => {
  it('renders nothing while capabilities are still loading', () => {
    capsMocks.current = null;
    const { container } = render(<OAuthButtonRow mode="signup" />, { wrapper: Wrapper });
    expect(container.firstChild).toBeNull();
  });

  it('renders nothing when no OIDC provider is enabled', () => {
    capsMocks.current = {
      passwordLogin: true,
      oidcGoogle: false,
      oidcGithub: false,
      oidcMicrosoft: false,
      magicLink: false,
      totp: false,
      registrationOpen: true,
    };
    const { container } = render(<OAuthButtonRow mode="signup" />, { wrapper: Wrapper });
    expect(container.firstChild).toBeNull();
  });

  it('renders one button per enabled provider with mode="signup"', () => {
    capsMocks.current = {
      passwordLogin: true,
      oidcGoogle: true,
      oidcGithub: true,
      oidcMicrosoft: false,
      magicLink: false,
      totp: false,
      registrationOpen: true,
    };
    render(<OAuthButtonRow mode="signup" />, { wrapper: Wrapper });

    expect(screen.queryByRole('button', { name: enAuth.login.sso_google })).not.toBeNull();
    expect(screen.queryByRole('button', { name: enAuth.login.sso_github })).not.toBeNull();
    // Microsoft is disabled in caps — must not render.
    expect(screen.queryByRole('button', { name: enAuth.login.sso_microsoft })).toBeNull();
  });

  it('exposes the mode discriminator as data-mode on the root element', () => {
    capsMocks.current = {
      passwordLogin: true,
      oidcGoogle: true,
      oidcGithub: false,
      oidcMicrosoft: false,
      magicLink: false,
      totp: false,
      registrationOpen: true,
    };
    const { container } = render(<OAuthButtonRow mode="signup" />, { wrapper: Wrapper });
    const root = container.querySelector('[data-mode]');
    expect(root).not.toBeNull();
    expect(root?.getAttribute('data-mode')).toBe('signup');
  });

  it('redirects via window.location when the provider start succeeds', async () => {
    capsMocks.current = {
      passwordLogin: true,
      oidcGoogle: true,
      oidcGithub: false,
      oidcMicrosoft: false,
      magicLink: false,
      totp: false,
      registrationOpen: true,
    };
    sdkMocks.get.mockResolvedValue({
      data: { authorizationUrl: 'https://oidc.example/start' },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    // window.location is read-only; replace `href` setter with a spy.
    const originalLocation = window.location;
    const setHrefSpy = vi.fn();
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...originalLocation,
        get href() {
          return originalLocation.href;
        },
        set href(value: string) {
          setHrefSpy(value);
        },
      },
    });

    try {
      render(<OAuthButtonRow mode="signup" />, { wrapper: Wrapper });
      await userEvent.click(screen.getByRole('button', { name: enAuth.login.sso_google }));
      expect(sdkMocks.get).toHaveBeenCalledWith('/auth/oidc/google/start');
      expect(setHrefSpy).toHaveBeenCalledWith('https://oidc.example/start');
    } finally {
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: originalLocation,
      });
    }
  });

  it('carries the caller redirect across the provider hand-off', async () => {
    // The page is discarded the moment we navigate to the provider, so
    // the target has to be stashed before that happens.
    const target = 'http://localhost:5173/workspaces/w1/tasks/t1';
    capsMocks.current = googleOnlyCaps();
    sdkMocks.get.mockResolvedValue({
      data: { authorizationUrl: 'https://oidc.example/start' },
      error: null,
      response: new Response(null, { status: 200 }),
    });
    const location = stubLocationHref();
    try {
      render(<OAuthButtonRow mode="login" redirect={target} />, { wrapper: Wrapper });
      await userEvent.click(screen.getByRole('button', { name: enAuth.login.sso_google }));
      expect(location.setHref).toHaveBeenCalledWith('https://oidc.example/start');
      expect(takeOidcRedirect()).toBe(target);
    } finally {
      location.restore();
    }
  });

  it('does not carry a redirect to an outside origin', async () => {
    capsMocks.current = googleOnlyCaps();
    sdkMocks.get.mockResolvedValue({
      data: { authorizationUrl: 'https://oidc.example/start' },
      error: null,
      response: new Response(null, { status: 200 }),
    });
    const location = stubLocationHref();
    try {
      render(<OAuthButtonRow mode="login" redirect="https://evil.example/steal" />, {
        wrapper: Wrapper,
      });
      await userEvent.click(screen.getByRole('button', { name: enAuth.login.sso_google }));
      // The sign-in still starts; only the unusable target is dropped.
      expect(location.setHref).toHaveBeenCalledWith('https://oidc.example/start');
      expect(takeOidcRedirect()).toBeNull();
    } finally {
      location.restore();
    }
  });

  it('reports errors to the parent via onError', async () => {
    capsMocks.current = {
      passwordLogin: true,
      oidcGoogle: true,
      oidcGithub: false,
      oidcMicrosoft: false,
      magicLink: false,
      totp: false,
      registrationOpen: true,
    };
    sdkMocks.get.mockResolvedValue({
      data: null,
      error: { type: 'AUTH.OIDC.UNAVAILABLE', detail: 'oops' },
      response: new Response(null, { status: 400 }),
    });
    const onError = vi.fn();
    render(<OAuthButtonRow mode="signup" onError={onError} />, { wrapper: Wrapper });

    await userEvent.click(screen.getByRole('button', { name: enAuth.login.sso_google }));
    expect(onError).toHaveBeenCalled();
  });
});
