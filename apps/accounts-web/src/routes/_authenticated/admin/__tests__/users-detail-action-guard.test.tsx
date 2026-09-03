/**
 * `/admin/users/$userId` per-action guards.
 *
 * Before this fix all destructive actions on the user-detail page shared
 * a single `actionLoading` boolean. If the suspend mutation hung (slow
 * server, transient network), a user could click "grant admin" before
 * React re-rendered the disabled state and end up firing both
 * mutations concurrently. The fix splits the lock so each button has
 * its own `useSubmitGuard`. This spec proves the invariants:
 *
 *   - Clicking "grant admin" while a suspend mutation is in flight
 *     still queues the grant call (the guards are independent).
 *   - Clicking "suspend" twice during the same in-flight request fires
 *     the PATCH only once (the per-action guard remains active for its
 *     own button).
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../../../locales/en/admin.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}));

const confirmMock = vi.hoisted(() => ({
  fn: vi.fn(),
}));

vi.mock('../../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
    PATCH: sdkMocks.patch,
    DELETE: sdkMocks.delete,
  },

  authSdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
    PATCH: sdkMocks.patch,
    DELETE: sdkMocks.delete,
  },
}));

vi.mock('@nodate-flow/ui/primitives/confirm/action', () => ({
  confirmAction: confirmMock.fn,
}));

// Stub createFileRoute + Link so the page module loads without a real
// router. `Route.useParams()` resolves through the synthetic options
// object's `useParams` field that the production `createFileRoute`
// returns; we hand-roll an equivalent shape here.
vi.mock('@tanstack/react-router', () => {
  return {
    createFileRoute: () => () => ({
      options: {},
      useParams: () => ({ userId: 'u-1' }),
    }),
    Link: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => (
      <a {...(rest as Record<string, unknown>)}>{children}</a>
    ),
    // The danger-zone delete handler navigates to /admin/users on
    // success. The test never exercises the delete flow, but the
    // page still calls `useNavigate()` at render time so the hook
    // must resolve to a no-op shaped like the real return value.
    useNavigate: () => () => undefined,
  };
});

const { UserDetailPage } = await import('../users_.$userId');

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

function mountUserDetail(): void {
  const testI18n = buildI18n();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={qc}>
        <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>
      </QueryClientProvider>
    );
  }
  render(<UserDetailPage />, { wrapper: Wrapper });
}

beforeAll(() => {
  if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => undefined,
        removeListener: () => undefined,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
        dispatchEvent: () => false,
      }),
    });
  }
});

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.patch.mockReset();
  sdkMocks.delete.mockReset();
  confirmMock.fn.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

const baseUser = {
  id: 'u-1',
  email: 'target@example.test',
  displayName: 'Target User',
  enabled: true,
  isInstanceAdmin: false,
  workspaceCount: 0,
  lastLoginAt: null,
  createdAt: 1700000000,
};

/** Wire the initial GET pair (user + sessions) the page issues on mount. */
function primeInitialFetch(): void {
  sdkMocks.get.mockImplementation(async (path: string) => {
    if (path === '/admin/users/{userId}') {
      return { data: baseUser, error: null, response: new Response(null, { status: 200 }) };
    }
    if (path === '/admin/users/{userId}/sessions') {
      return {
        data: { items: [], total: 0 },
        error: null,
        response: new Response(null, { status: 200 }),
      };
    }
    return {
      data: null,
      error: { type: 'unhandled' },
      response: new Response(null, { status: 400 }),
    };
  });
}

describe('user-detail per-action guard', () => {
  it('clicking suspend twice fires PATCH once', async () => {
    primeInitialFetch();
    confirmMock.fn.mockResolvedValue(true);

    let resolvePatch: (v: unknown) => void = () => undefined;
    sdkMocks.patch.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolvePatch = resolve;
        }),
    );

    mountUserDetail();

    const suspendBtn = await screen.findByRole('button', { name: enAdmin.users.suspend });

    // First click opens the confirm dialog (mocked to resolve true) and
    // then issues the PATCH. The async confirm resolution gives the
    // event loop a tick — exactly the window we need to verify the
    // guard. Wait for PATCH to actually fire, then click again
    // synchronously: the per-action guard should bail.
    await userEvent.click(suspendBtn);
    await waitFor(() => expect(sdkMocks.patch).toHaveBeenCalledTimes(1));

    await act(async () => {
      suspendBtn.click();
    });

    // Second synchronous click bails at the guard.
    expect(sdkMocks.patch).toHaveBeenCalledTimes(1);

    resolvePatch({ data: baseUser, error: null });
  });

  it('grant admin during an in-flight suspend still fires its own POST (independent guards)', async () => {
    primeInitialFetch();
    // `confirmAction` resolves true for suspend; the grant-admin path
    // does not call confirmAction (additive, no confirm by design) so
    // its mock is unused.
    confirmMock.fn.mockResolvedValue(true);

    // Park the suspend PATCH so the suspend guard stays busy.
    sdkMocks.patch.mockImplementation(() => new Promise(() => undefined));
    // Resolve the grant POST immediately.
    sdkMocks.post.mockResolvedValue({
      data: { admin: { id: 'u-1' } },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    mountUserDetail();

    const suspendBtn = await screen.findByRole('button', { name: enAdmin.users.suspend });
    const grantBtn = await screen.findByRole('button', { name: enAdmin.users.grant_admin });

    await userEvent.click(suspendBtn);
    await waitFor(() => expect(sdkMocks.patch).toHaveBeenCalledTimes(1));

    // Suspend button is disabled but grant remains clickable because
    // the guards are independent.
    expect((suspendBtn as HTMLButtonElement).disabled).toBe(true);
    expect((grantBtn as HTMLButtonElement).disabled).toBe(false);

    await userEvent.click(grantBtn);
    await waitFor(() => expect(sdkMocks.post).toHaveBeenCalledTimes(1));
    expect(sdkMocks.post).toHaveBeenCalledWith('/admin/instance-admins', {
      body: { userId: 'u-1' },
    });
  });
});
