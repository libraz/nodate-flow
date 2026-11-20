/**
 * WorkspaceSwitcher: one button + menu for any number of workspaces.
 *
 * `/`, `/calendar`, `/today`, `/inbox`, `/settings` and `/pages` carry no
 * workspace id in the path, so switching there cannot be expressed as a
 * navigation. The switcher has to commit the choice itself; if it does
 * not, the menu closes and nothing on the page moves.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
  pathname: '/calendar',
  workspaces: [] as { id: string; name: string }[],
  sdkGet: vi.fn(),
}));

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    params,
    children,
    ...rest
  }: {
    to: string;
    params?: { id: string };
    children: ReactNode;
  } & Record<string, unknown>) => (
    <a href={params ? to.replace('$id', params.id) : to} {...rest}>
      {children}
    </a>
  ),
  useNavigate: () => mocks.navigate,
  // Both the switcher and useCurrentWorkspaceId read the pathname
  // through a selector, so the fake state only needs `location`.
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: mocks.pathname } }),
}));

vi.mock('../../../lib/sdk', () => ({
  // openapi-fetch keys its client by HTTP method, hence the caps.
  sdk: { GET: mocks.sdkGet },
  authSdk: { GET: mocks.sdkGet },
}));

vi.mock('../../../features/workspaces/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../features/workspaces/api')>()),
  useWorkspacesQuery: () => ({
    data: mocks.workspaces,
    response: new Response(null, { status: 200 }),
  }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { name?: string }) => (opts?.name ? `${key}:${opts.name}` : key),
  }),
}));

import { clearActiveWorkspaceId, setActiveWorkspaceId } from '../../../lib/use-current-workspace';
import WorkspaceSwitcher from '../workspace-switcher';

const STORAGE_KEY = 'nf.activeWsId';

function renderSwitcher(client: QueryClient): void {
  render(
    <QueryClientProvider client={client}>
      <WorkspaceSwitcher />
    </QueryClientProvider>,
  );
}

const newClient = (): QueryClient =>
  new QueryClient({ defaultOptions: { queries: { retry: false } } });

const TWO = [
  { id: 'ws-1', name: 'Acme' },
  { id: 'ws-2', name: 'Globex' },
];

beforeEach(() => {
  mocks.workspaces = TWO;
  mocks.navigate.mockReset();
  // `useCurrentWorkspaceId` purges a stored id that is no longer in the
  // member list, so this has to agree with the switcher's rows.
  mocks.sdkGet.mockReset().mockResolvedValue({
    data: { workspaces: [{ id: 'ws-1' }, { id: 'ws-2' }] },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  mocks.pathname = '/calendar';
  window.localStorage.clear();
  // The stored id is cached in module scope so reads stay off the
  // synchronous storage API; clear it too or the cache outlives the test.
  clearActiveWorkspaceId();
});

async function openMenu(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByRole('button', { name: 'nav.switchWorkspace' }));
}

describe.each(['/', '/calendar', '/today'])(
  'WorkspaceSwitcher on cross-workspace route %s',
  (path) => {
    beforeEach(() => {
      mocks.pathname = path;
    });

    it('persists the chosen workspace so the switch survives the render', async () => {
      const user = userEvent.setup();
      renderSwitcher(newClient());
      await openMenu(user);

      await user.click(screen.getByRole('menuitemradio', { name: /Globex/ }));

      expect(window.localStorage.getItem(STORAGE_KEY)).toBe('ws-2');
    });

    it('refreshes what is on screen instead of leaving another workspace rendered', async () => {
      const user = userEvent.setup();
      const client = newClient();
      const invalidate = vi.spyOn(client, 'invalidateQueries');
      renderSwitcher(client);
      await openMenu(user);

      await user.click(screen.getByRole('menuitemradio', { name: /Globex/ }));

      expect(invalidate).toHaveBeenCalledWith({ refetchType: 'active' });
    });

    it('stays on the page rather than navigating somewhere workspace-scoped', async () => {
      const user = userEvent.setup();
      renderSwitcher(newClient());
      await openMenu(user);

      await user.click(screen.getByRole('menuitemradio', { name: /Globex/ }));

      expect(mocks.navigate).not.toHaveBeenCalled();
    });
  },
);

describe('WorkspaceSwitcher on a workspace-scoped route', () => {
  it('navigates to the chosen workspace project list', async () => {
    mocks.pathname = '/workspaces/ws-1/projects';
    const user = userEvent.setup();
    renderSwitcher(newClient());
    await openMenu(user);

    await user.click(screen.getByRole('menuitemradio', { name: /Globex/ }));

    expect(mocks.navigate).toHaveBeenCalledWith({
      to: '/workspaces/$id/projects',
      params: { id: 'ws-2' },
    });
  });
});

describe('WorkspaceSwitcher menu', () => {
  it('has the same button and menu for a single workspace', async () => {
    mocks.workspaces = [{ id: 'ws-1', name: 'acme' }];
    const user = userEvent.setup();
    renderSwitcher(newClient());

    expect(screen.queryByRole('combobox')).toBeNull();
    const button = screen.getByRole('button', { name: 'nav.switchWorkspace' });
    expect(button.getAttribute('aria-haspopup')).toBe('menu');
    expect(button.textContent).toContain('acme');
    expect(button.textContent).toContain('A');

    await user.click(button);

    expect(screen.getByRole('menu')).toBeTruthy();
    expect(screen.getAllByRole('menuitemradio')).toHaveLength(1);
  });

  it('marks the current workspace as checked', async () => {
    setActiveWorkspaceId('ws-2');
    const user = userEvent.setup();
    renderSwitcher(newClient());
    await openMenu(user);

    const rows = screen.getAllByRole('menuitemradio');
    expect(rows).toHaveLength(2);
    expect(screen.getByRole('menuitemradio', { name: /Globex/ }).getAttribute('aria-checked')).toBe(
      'true',
    );
    expect(screen.getByRole('menuitemradio', { name: /Acme/ }).getAttribute('aria-checked')).toBe(
      'false',
    );
  });

  it('offers all workspaces and a new workspace below the rows', async () => {
    const user = userEvent.setup();
    renderSwitcher(newClient());
    await openMenu(user);

    const all = screen.getByRole('menuitem', { name: 'nav.allWorkspaces' });
    expect(all.getAttribute('href')).toBe('/workspaces');
    expect(screen.getByRole('menuitem', { name: 'nav.newWorkspace' })).toBeTruthy();
  });

  it('renders nothing when there are no workspaces', () => {
    mocks.workspaces = [];
    renderSwitcher(newClient());

    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
  });
});
