/**
 * A forbidden workspace-scoped list hides its sidebar section, not the shell.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  pathname: '/today',
  workspaces: [] as Array<{ id: string; name: string }>,
  role: null as string | null,
  projects: [] as Array<{ id: string; name: string; isArchived: boolean }>,
  favorites: [] as Array<{
    id: string;
    targetType: string;
    targetId: string;
    title: string | null;
  }>,
}));

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useMatch: () => undefined,
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: mocks.pathname } }),
  Link: ({
    to,
    params,
    children,
    activeProps: _activeProps,
    activeOptions: _activeOptions,
    ...rest
  }: {
    to: string;
    params?: Record<string, string>;
    children: ReactNode;
    activeProps?: unknown;
    activeOptions?: unknown;
  } & Record<string, unknown>) => {
    const href = params
      ? Object.entries(params).reduce((acc, [key, value]) => acc.replace(`$${key}`, value), to)
      : to;
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    );
  },
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: vi.fn() },
  authSdk: { GET: vi.fn() },
}));

vi.mock('../../../lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/api')>()),
  authApiRequest: async (
    call: (client: {
      GET: (path: string) => Promise<{ data: unknown }>;
    }) => Promise<{ data: unknown }>,
  ) => {
    const result = await call({
      GET: async (path: string) => ({
        data: path === '/workspaces' ? { workspaces: mocks.workspaces } : {},
      }),
    });
    return result.data;
  },
}));

vi.mock('../../../features/projects/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../features/projects/api')>()),
  useProjectsQuery: () => {
    throw new ApiError('FORBIDDEN', 'forbidden', 403);
  },
}));

vi.mock('../../../features/favorites/api', () => ({
  useFavoritesQuery: () => ({
    data: mocks.favorites,
    response: new Response(null, { status: 200 }),
  }),
}));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { ApiError } from '../../../lib/api-error';
import { clearActiveWorkspaceId, setActiveWorkspaceId } from '../../../lib/use-current-workspace';
import Sidebar from '../sidebar';
import styles from '../sidebar.module.css';

function renderSidebar(): void {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, throwOnError: true } },
  });
  client.setQueryData(['workspaces', 'list'], [{ id: 'ws-1', name: 'Acme' }]);
  render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  clearActiveWorkspaceId();
  setActiveWorkspaceId('ws-1');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('sidebar workspace section failure', () => {
  it('keeps the personal nav and workspace links when the projects query is forbidden', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    renderSidebar();

    expect(await screen.findByRole('heading', { name: 'Acme' })).toBeTruthy();
    expect(screen.getByRole('link', { name: /nav\.today/ })).toBeTruthy();
    expect(screen.getByRole('link', { name: /nav\.members/ })).toBeTruthy();
  });
});

describe('sidebar workspace the user is not a member of', () => {
  beforeEach(() => {
    mocks.pathname = '/workspaces/ws-2';
    mocks.workspaces = [{ id: 'ws-1', name: 'Acme' }];
  });

  afterEach(() => {
    mocks.pathname = '/today';
    mocks.workspaces = [];
  });

  it('renders no workspace skeleton once the list has loaded without the URL workspace', async () => {
    renderSidebar();

    expect(await screen.findByRole('link', { name: /nav\.today/ })).toBeTruthy();
    // Workspace-group links are scoped to the URL workspace, so they are absent with it.
    expect(screen.queryByRole('link', { name: /nav\.members/ })).toBeNull();
    // Guard against an unresolved CSS module key turning the selector into `.undefined`.
    expect(styles.headingSkeleton).toBeTruthy();
    expect(document.querySelector(`.${styles.headingSkeleton}`)).toBeNull();
  });
});
