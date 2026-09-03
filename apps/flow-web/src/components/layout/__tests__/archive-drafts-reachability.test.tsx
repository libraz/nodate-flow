/**
 * Entry points for the Archive Room and the retrospective-draft queue.
 *
 * Both routes existed and worked, and neither was linked from anywhere:
 * the only way in was to type the URL. That makes archiving a one-way
 * door in practice and leaves the AI-filled draft queue accumulating out
 * of sight. E2E specs reach these pages with `page.goto()`, so they stay
 * green while the pages are unreachable — hence these assertions are
 * about the presence of a link, not about the pages themselves.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  pathname: '/workspaces/ws-1',
  sdkGet: vi.fn(),
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
  }: {
    to: string;
    params?: Record<string, string>;
    children: ReactNode;
  }): ReactElement => {
    // Resolve `$id`-style path params so the assertions can check the
    // destination the user would actually land on.
    const href = params
      ? Object.entries(params).reduce((acc, [key, value]) => acc.replace(`$${key}`, value), to)
      : to;
    return <a href={href}>{children}</a>;
  },
}));

vi.mock('../../../lib/sdk', () => ({
  // openapi-fetch keys its client by HTTP method, hence the caps.
  sdk: { GET: mocks.sdkGet },
  authSdk: { GET: mocks.sdkGet },
}));

vi.mock('../../../features/projects/api', () => ({
  useProjectsQuery: () => ({ data: [], response: new Response(null, { status: 200 }) }),
  response: new Response(null, { status: 200 }),
}));

vi.mock('../../../features/favorites/api', () => ({
  useFavoritesQuery: () => ({ data: [], response: new Response(null, { status: 200 }) }),
  response: new Response(null, { status: 200 }),
}));

vi.mock('../../../features/workspaces/api', () => ({
  useWorkspacesQuery: () => ({
    data: mocks.workspaces,
    response: new Response(null, { status: 200 }),
  }),
  response: new Response(null, { status: 200 }),
}));

vi.mock('../../../features/nl-command/api', () => ({
  useResolveCommand: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { clearActiveWorkspaceId } from '../../../lib/use-current-workspace';
import CommandPalette from '../command-palette';
import Sidebar from '../sidebar';

function renderSidebar(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mocks.pathname = '/workspaces/ws-1';
  mocks.workspaces = [{ id: 'ws-1', name: 'Acme' }];
  mocks.sdkGet.mockReset().mockResolvedValue({
    data: { workspaces: [{ id: 'ws-1' }] },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  window.localStorage.clear();
  clearActiveWorkspaceId();
});

describe('sidebar workspace navigation', () => {
  it('links to the Archive Room', () => {
    renderSidebar();

    const link = screen.getByText('nav.archive').closest('a');
    expect(link).not.toBeNull();
    expect(link?.getAttribute('href')).toBe('/workspaces/ws-1/tasks/archived');
  });

  it('links to the retrospective-draft queue', () => {
    renderSidebar();

    const link = screen.getByText('nav.retroDrafts').closest('a');
    expect(link).not.toBeNull();
    expect(link?.getAttribute('href')).toBe('/workspaces/ws-1/tasks/drafts');
  });

  it('keeps them beside the other workspace destinations', () => {
    renderSidebar();

    // They belong to the same workspace sub-nav as the surfaces that
    // were already wired up; a link nobody expects to find there is
    // barely better than no link.
    for (const key of ['nav.activity', 'nav.timeboxes', 'nav.insightsPriority']) {
      expect(screen.getByText(key)).toBeDefined();
    }
  });
});

describe('command palette', () => {
  it('offers both destinations by name', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <CommandPalette open onClose={vi.fn()} />
      </QueryClientProvider>,
    );

    const archive = await screen.findByRole('button', { name: /nav\.archive/ });
    const drafts = await screen.findByRole('button', { name: /nav\.retroDrafts/ });
    expect(archive).toBeDefined();
    expect(drafts).toBeDefined();
  });

  it('omits them when no workspace is in context, since the paths cannot be built', async () => {
    // Nothing to interpolate into `/workspaces/$id/tasks/...`: no
    // workspace in the URL and none to fall back to.
    mocks.pathname = '/login';
    mocks.workspaces = [];
    mocks.sdkGet.mockResolvedValue({
      data: { workspaces: [] },
      error: null,
      response: new Response(null, { status: 200 }),
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <CommandPalette open onClose={vi.fn()} />
      </QueryClientProvider>,
    );

    await screen.findByRole('button', { name: /nav\.today/ });
    expect(screen.queryByRole('button', { name: /nav\.archive/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /nav\.retroDrafts/ })).toBeNull();
  });
});
