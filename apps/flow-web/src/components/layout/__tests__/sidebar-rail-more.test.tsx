/**
 * Collapsed rail: the More destinations live behind one icon button that
 * opens a popover and closes again on selection.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  pathname: '/today',
}));

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
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
        data: path === '/workspaces' ? { workspaces: [{ id: 'ws-1', name: 'Acme' }] } : {},
      }),
    });
    return result.data;
  },
}));

vi.mock('../../../features/projects/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../features/projects/api')>()),
  useProjectsQuery: () => ({ data: [], response: new Response(null, { status: 200 }) }),
}));

vi.mock('../../../features/favorites/api', () => ({
  useFavoritesQuery: () => ({ data: [], response: new Response(null, { status: 200 }) }),
}));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => null,
  isWorkspaceAdmin: () => false,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { clearActiveWorkspaceId, setActiveWorkspaceId } from '../../../lib/use-current-workspace';
import { navEntriesIn } from '../nav-model';
import Sidebar from '../sidebar';

const MORE_LABELS = navEntriesIn('more').map((e) => e.labelKey);

function renderSidebar(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(['workspaces', 'list'], [{ id: 'ws-1', name: 'Acme' }]);
  render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mocks.pathname = '/today';
  window.localStorage.clear();
  window.localStorage.setItem('nf.sidebar-collapsed', '1');
  clearActiveWorkspaceId();
  setActiveWorkspaceId('ws-1');
});

describe('collapsed rail More', () => {
  it('opens a popover with the More destinations', async () => {
    const user = userEvent.setup();
    renderSidebar();
    const trigger = await screen.findByRole('button', { name: 'nav.more' });
    expect(screen.queryByRole('link', { name: 'nav.history' })).toBeNull();

    await user.click(trigger);

    const dialog = await screen.findByRole('dialog', { name: 'nav.more' });
    expect(MORE_LABELS).toHaveLength(4);
    for (const name of MORE_LABELS) {
      expect(dialog.querySelector(`a[aria-label="${name}"]`)).not.toBeNull();
    }
  });

  it('closes after a destination is chosen', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(await screen.findByRole('button', { name: 'nav.more' }));
    const dialog = await screen.findByRole('dialog', { name: 'nav.more' });

    await user.click(dialog.querySelector('a[aria-label="nav.history"]') as HTMLElement);

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: 'nav.more' })).toBeNull();
    });
  });

  it('marks the trigger active under a More destination', async () => {
    mocks.pathname = '/workspaces/ws-1/timeboxes';
    renderSidebar();

    const trigger = await screen.findByRole('button', { name: 'nav.more' });
    expect(trigger.className).toContain('itemActive');
  });

  it('leaves the trigger inactive elsewhere', async () => {
    renderSidebar();

    const trigger = await screen.findByRole('button', { name: 'nav.more' });
    expect(trigger.className).not.toContain('itemActive');
  });
});
