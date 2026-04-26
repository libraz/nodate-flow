/**
 * Component tests for the root FatalFallback in accounts-web.
 *
 * Verifies that the fatal error fallback translates ApiError codes via the
 * shared `errors` namespace (so JA/ZH users do not see raw English copy)
 * while still rendering plain Error messages and unknown thrown values
 * verbatim.
 *
 * Mirrors the behaviour added to flow-web's FatalFallback in commit
 * cac5df3 — without this parity fix, sign-in / OIDC failures surfaced
 * by the auth API would leak English error detail to localised users.
 */

import { ApiError } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from '@tanstack/react-router';
import { type RenderResult, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, describe, expect, it } from 'vitest';

import enAuth from '../../locales/en/auth.json';
import enErrors from '../../locales/en/errors.json';
import jaAuth from '../../locales/ja/auth.json';
import jaErrors from '../../locales/ja/errors.json';
import zhAuth from '../../locales/zh/auth.json';
import zhErrors from '../../locales/zh/errors.json';
import { FatalFallback } from './__root';

/**
 * Build a fresh test-only i18next instance configured exactly like the
 * production accounts-web instance (auth + errors namespaces in en/ja/zh).
 * A separate instance per test avoids cross-test language pollution.
 */
function buildTestI18n(lng: 'en' | 'ja' | 'zh'): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng,
      fallbackLng: 'en',
      defaultNS: 'auth',
      ns: ['auth', 'errors'],
      resources: {
        en: { auth: enAuth, errors: enErrors },
        ja: { auth: jaAuth, errors: jaErrors },
        zh: { auth: zhAuth, errors: zhErrors },
      },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return instance;
}

/**
 * FatalFallback uses `useRouterState`, which requires an active TanStack
 * Router context. We mount it inside an in-memory router whose root route
 * renders the fallback with the supplied error. The `Wrapper` adds the
 * Query + i18n providers the rest of the app expects.
 */
function renderFallback(error: unknown, lng: 'en' | 'ja' | 'zh' = 'en'): RenderResult {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  const testI18n = buildTestI18n(lng);

  const rootRoute = createRootRoute({
    component: () => <FatalFallback error={error} resetErrorBoundary={() => {}} />,
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }

  return render(<RouterProvider router={router} />, { wrapper: Wrapper });
}

beforeAll(() => {
  // happy-dom doesn't always implement scrollTo; TanStack Router's hash
  // restoration tries to call it during memory history navigation.
  if (typeof window !== 'undefined' && typeof window.scrollTo !== 'function') {
    window.scrollTo = (() => {}) as typeof window.scrollTo;
  }
});

afterEach(() => {
  // No global state to reset — each test creates its own i18n instance,
  // QueryClient, and router. This hook exists for future-proofing only.
});

describe('<FatalFallback>', () => {
  it('translates ApiError.code via the errors namespace when locale is ja', async () => {
    const err = new ApiError('AUTH.LOGIN.INVALID_CREDENTIALS', 'Invalid email or password', 401);
    renderFallback(err, 'ja');
    const expected = jaErrors['AUTH.LOGIN.INVALID_CREDENTIALS'];
    // Sanity: the JA copy must differ from the EN fallback (otherwise the
    // assertion would pass even if translation never happened).
    expect(expected).not.toBe('Invalid email or password');
    const node = await screen.findByText(expected);
    expect(node).toBeDefined();
  });

  it('renders plain Error.message verbatim', async () => {
    renderFallback(new Error('boom'), 'ja');
    const node = await screen.findByText('boom');
    expect(node).toBeDefined();
  });

  it('renders unknown thrown values via String(error)', async () => {
    renderFallback('plain string', 'ja');
    const node = await screen.findByText('plain string');
    expect(node).toBeDefined();
  });

  it('falls back to ApiError.message when the code has no translation', async () => {
    const err = new ApiError('UNKNOWN.MADE.UP.CODE', 'fallback detail', 500);
    renderFallback(err, 'ja');
    const node = await screen.findByText('fallback detail');
    expect(node).toBeDefined();
  });
});
