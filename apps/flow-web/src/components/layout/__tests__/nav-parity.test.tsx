/**
 * Every destination in `NAV_ENTRIES` must be reachable from the command
 * palette (by the same label) and from the sidebar or the workspace
 * switcher menu. A destination present in only one of them is the defect
 * this model exists to prevent.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  pathname: '/today',
  workspaces: [{ id: 'ws-1', name: 'Acme' }] as Array<{ id: string; name: string }>,
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
  authSdk: {
    GET: vi.fn(async () => ({
      data: { workspaces: [{ id: 'ws-1' }] },
      error: null,
      response: new Response(null, { status: 200 }),
    })),
  },
}));

vi.mock('../../../features/projects/api', () => ({
  useProjectsQuery: () => ({ data: [], response: new Response(null, { status: 200 }) }),
}));

vi.mock('../../../features/favorites/api', () => ({
  useFavoritesQuery: () => ({ data: [], response: new Response(null, { status: 200 }) }),
}));

vi.mock('../../../features/workspaces/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../features/workspaces/api')>()),
  useWorkspacesQuery: () => ({
    data: mocks.workspaces,
    response: new Response(null, { status: 200 }),
  }),
}));

vi.mock('../../../features/nl-command/api', () => ({
  useResolveCommand: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => null,
  isWorkspaceAdmin: () => false,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { clearActiveWorkspaceId, setActiveWorkspaceId } from '../../../lib/use-current-workspace';
import CommandPalette from '../command-palette';
import { NAV_ENTRIES, navHref } from '../nav-model';
import Sidebar from '../sidebar';

function wrap(node: ReactNode): ReactNode {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}

beforeEach(() => {
  mocks.pathname = '/today';
  window.localStorage.clear();
  clearActiveWorkspaceId();
  setActiveWorkspaceId('ws-1');
});

describe('NAV_ENTRIES model', () => {
  it('has a route placeholder exactly when it needs a workspace', () => {
    for (const entry of NAV_ENTRIES) {
      expect(entry.route.includes('$id')).toBe(entry.needsWorkspace);
    }
  });

  it('resolves hrefs and refuses a workspace entry without a workspace', () => {
    for (const entry of NAV_ENTRIES) {
      const href = navHref(entry, 'ws-1');
      expect(href).not.toBeNull();
      expect(href).not.toContain('$id');
      expect(navHref(entry, null)).toBe(entry.needsWorkspace ? null : href);
    }
  });

  it('gives every low-frequency entry, the archive and the reminders a hint', () => {
    for (const entry of NAV_ENTRIES) {
      if (entry.group === 'more' || entry.key === 'archive' || entry.key === 'reminders') {
        expect(entry.hintKey).toBeDefined();
      }
    }
  });
});

describe('palette and sidebar render the same destinations', () => {
  it('offers every entry in the command palette under its label', async () => {
    render(wrap(<CommandPalette open onClose={vi.fn()} />));

    for (const entry of NAV_ENTRIES) {
      const found = await screen.findByRole('button', {
        name: new RegExp(entry.labelKey.replace(/\./g, '\\.')),
      });
      expect(found).toBeDefined();
    }
  });

  it('renders every entry in the sidebar or the switcher menu', async () => {
    const user = userEvent.setup();
    render(wrap(<Sidebar />));
    await screen.findByRole('heading', { name: 'Acme' });
    await user.click(screen.getByRole('button', { name: 'nav.more' }));
    await user.click(screen.getByRole('button', { name: 'nav.switchWorkspace' }));

    for (const entry of NAV_ENTRIES) {
      const candidates = screen
        .queryAllByText(entry.labelKey)
        .filter((el) => el.closest('a') !== null);
      expect(candidates.length, entry.labelKey).toBeGreaterThan(0);
    }
  });

  it('puts the all-workspaces entry in the switcher menu, not the sidebar body', async () => {
    const user = userEvent.setup();
    render(wrap(<Sidebar />));
    await screen.findByRole('heading', { name: 'Acme' });
    expect(screen.queryByText('nav.allWorkspaces')).toBeNull();

    await user.click(screen.getByRole('button', { name: 'nav.switchWorkspace' }));

    expect(screen.getByRole('menuitem', { name: 'nav.allWorkspaces' })).toBeTruthy();
  });
});
