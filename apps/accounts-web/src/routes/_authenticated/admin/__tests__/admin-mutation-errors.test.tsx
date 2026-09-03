/**
 * A refused admin mutation must not leave the page showing the change
 * as if it had happened.
 *
 * These screens act on other people's accounts: suspending a user,
 * revoking an instance-admin grant, killing a session. Painting the new
 * state locally and skipping the refetch when the server said no leaves
 * the operator believing an account is locked out while it is still
 * signed in — and the next operator reads the same screen.
 *
 * The check is on the rendered outcome, not on the shape of the call
 * site, so it survives the call being rewritten.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
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

const confirmMock = vi.hoisted(() => ({ fn: vi.fn() }));
const toasterMock = vi.hoisted(() => ({ show: vi.fn() }));

vi.mock('../../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
    PATCH: sdkMocks.patch,
    DELETE: sdkMocks.delete,
  },
}));

vi.mock('@nodate-flow/ui/primitives/confirm/action', () => ({
  confirmAction: confirmMock.fn,
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: toasterMock,
  default: () => null,
  ToastProvider: () => null,
}));

vi.mock('@tanstack/react-router', () => {
  return {
    createFileRoute: () => () => ({
      options: {},
      useParams: () => ({ userId: 'u-1' }),
    }),
    Link: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => (
      <a {...(rest as Record<string, unknown>)}>{children}</a>
    ),
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

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.patch.mockReset();
  sdkMocks.delete.mockReset();
  confirmMock.fn.mockReset();
  toasterMock.show.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('refused admin mutations', () => {
  it('reports the refusal with its code and does not refetch as if it had worked', async () => {
    primeInitialFetch();
    confirmMock.fn.mockResolvedValue(true);
    sdkMocks.patch.mockResolvedValue({
      data: null,
      error: { type: 'ADMIN.USER.SELF_SUSPEND', title: 'Forbidden', detail: 'no', status: 403 },
      response: new Response(null, { status: 403 }),
    });

    mountUserDetail();
    const suspend = await screen.findByRole('button', { name: enAdmin.users.suspend });
    const readsBeforeAction = sdkMocks.get.mock.calls.length;

    await userEvent.click(suspend);

    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    const toast = toasterMock.show.mock.calls[0]?.[0];
    expect(toast?.tone).toBe('danger');
    expect(String(toast?.message)).toContain('ADMIN.USER.SELF_SUSPEND');

    // The page still offers to suspend: nothing was applied locally.
    expect(screen.queryByRole('button', { name: enAdmin.users.suspend })).not.toBeNull();
    expect(sdkMocks.get.mock.calls.length).toBe(readsBeforeAction);
  });

  it('reports a refusal that arrived without an error body', async () => {
    // A bodyless 405 or a gateway 502 carries no error to read, and a
    // handler that only looks at `error` walks straight into its
    // success path.
    primeInitialFetch();
    confirmMock.fn.mockResolvedValue(true);
    sdkMocks.patch.mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 502 }),
    });

    mountUserDetail();
    const suspend = await screen.findByRole('button', { name: enAdmin.users.suspend });
    const readsBeforeAction = sdkMocks.get.mock.calls.length;

    await userEvent.click(suspend);

    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    expect(toasterMock.show.mock.calls[0]?.[0]?.tone).toBe('danger');
    expect(sdkMocks.get.mock.calls.length).toBe(readsBeforeAction);
  });

  it('refetches once the suspend is accepted', async () => {
    primeInitialFetch();
    confirmMock.fn.mockResolvedValue(true);
    sdkMocks.patch.mockResolvedValue({
      data: { ok: true },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    mountUserDetail();
    const suspend = await screen.findByRole('button', { name: enAdmin.users.suspend });
    const readsBeforeAction = sdkMocks.get.mock.calls.length;

    await userEvent.click(suspend);

    await waitFor(() => {
      expect(sdkMocks.get.mock.calls.length).toBeGreaterThan(readsBeforeAction);
    });
    expect(toasterMock.show).not.toHaveBeenCalled();
  });
});
