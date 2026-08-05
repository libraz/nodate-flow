/**
 * WorkspaceSwitcher on cross-workspace routes.
 *
 * `/calendar`, `/today`, `/inbox`, `/settings` and `/pages` carry no
 * workspace id in the path, so switching there cannot be expressed as a
 * navigation. The switcher has to commit the choice itself; if it does
 * not, the control snaps back to the previous workspace and nothing on
 * the page moves — a dropdown that visibly does nothing.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
  pathname: '/calendar',
  sdkGet: vi.fn(),
}));

vi.mock('@tanstack/react-router', () => ({
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

vi.mock('../../../features/workspaces/api', () => ({
  useWorkspacesQuery: () => ({
    data: [
      { id: 'ws-1', name: 'Acme' },
      { id: 'ws-2', name: 'Globex' },
    ],
  }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { clearActiveWorkspaceId } from '../../../lib/use-current-workspace';
import WorkspaceSwitcher from '../workspace-switcher';

const STORAGE_KEY = 'nf.activeWsId';

function renderSwitcher(client: QueryClient): ReactElement {
  render(
    <QueryClientProvider client={client}>
      <WorkspaceSwitcher />
    </QueryClientProvider>,
  );
  return <WorkspaceSwitcher />;
}

beforeEach(() => {
  mocks.navigate.mockReset();
  // `useCurrentWorkspaceId` purges a stored id that is no longer in the
  // member list, so this has to agree with the switcher's options.
  mocks.sdkGet.mockReset().mockResolvedValue({
    data: { workspaces: [{ id: 'ws-1' }, { id: 'ws-2' }] },
    error: null,
  });
  mocks.pathname = '/calendar';
  window.localStorage.clear();
  // The stored id is cached in module scope so reads stay off the
  // synchronous storage API; clear it too or the cache outlives the test.
  clearActiveWorkspaceId();
});

describe('WorkspaceSwitcher on a cross-workspace route', () => {
  it('persists the chosen workspace so the switch survives the render', async () => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderSwitcher(client);

    await user.selectOptions(screen.getByRole('combobox'), 'ws-2');

    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('ws-2');
  });

  it('refreshes what is on screen instead of leaving another workspace rendered', async () => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    renderSwitcher(client);

    await user.selectOptions(screen.getByRole('combobox'), 'ws-2');

    expect(invalidate).toHaveBeenCalledWith({ refetchType: 'active' });
  });

  it('stays on the page rather than navigating somewhere workspace-scoped', async () => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderSwitcher(client);

    await user.selectOptions(screen.getByRole('combobox'), 'ws-2');

    expect(mocks.navigate).not.toHaveBeenCalled();
  });

  it('shows the newly chosen workspace as selected', async () => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderSwitcher(client);

    const select = screen.getByRole('combobox') as HTMLSelectElement;
    await user.selectOptions(select, 'ws-2');

    // The value is driven by useCurrentWorkspaceId, not by the DOM, so
    // this only holds if the write actually re-rendered the reader.
    expect(select.value).toBe('ws-2');
  });
});

describe('WorkspaceSwitcher on a workspace-scoped route', () => {
  it('navigates to the chosen workspace', async () => {
    mocks.pathname = '/workspaces/ws-1/projects';
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderSwitcher(client);

    await user.selectOptions(screen.getByRole('combobox'), 'ws-2');

    expect(mocks.navigate).toHaveBeenCalledWith({
      to: '/workspaces/$id',
      params: { id: 'ws-2' },
    });
  });
});
