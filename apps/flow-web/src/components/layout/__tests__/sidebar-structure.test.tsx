/**
 * Sidebar structure: the workspace is a standing section headed by its own
 * name, not a collapsed "Workspaces" entry that only opens on workspace URLs.
 *
 * `useCurrentWorkspaceId` is deliberately NOT mocked: the sidebar has to
 * resolve a workspace on `/today`, a route that carries no workspace id.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
  useProjectsQuery: () => ({ data: mocks.projects, response: new Response(null, { status: 200 }) }),
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

import { clearActiveWorkspaceId, setActiveWorkspaceId } from '../../../lib/use-current-workspace';
import { OPEN_SHORTCUTS_HELP_EVENT } from '../../page-help';
import Sidebar from '../sidebar';

const ACME = [{ id: 'ws-1', name: 'Acme' }];

function renderSidebar(): HTMLElement {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(['workspaces', 'list'], mocks.workspaces);
  return render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  ).container;
}

beforeEach(() => {
  mocks.pathname = '/today';
  mocks.workspaces = ACME;
  mocks.role = null;
  mocks.projects = [];
  mocks.favorites = [];
  window.localStorage.clear();
  clearActiveWorkspaceId();
  setActiveWorkspaceId('ws-1');
});

describe('sidebar on a route without a workspace id', () => {
  it('headlines the section with the workspace name', async () => {
    renderSidebar();

    expect(await screen.findByRole('heading', { name: 'Acme' })).toBeTruthy();
  });

  it('lists the workspace destinations beneath it', async () => {
    renderSidebar();
    await screen.findByRole('heading', { name: 'Acme' });

    const hrefs: Record<string, string> = {
      'nav.projects': '/workspaces/ws-1/projects',
      'nav.members': '/workspaces/ws-1/members',
      'nav.reminders': '/workspaces/ws-1/reminders',
      'nav.archive': '/workspaces/ws-1/tasks/archived',
      'nav.pages': '/pages',
    };
    for (const [name, href] of Object.entries(hrefs)) {
      expect(screen.getByRole('link', { name }).getAttribute('href')).toBe(href);
    }
    expect(screen.getByRole('button', { name: 'nav.more' })).toBeTruthy();
  });

  it('keeps the personal destinations above the workspace section', async () => {
    renderSidebar();
    await screen.findByRole('heading', { name: 'Acme' });

    for (const name of ['nav.home', 'nav.today', 'nav.inbox', 'nav.calendar']) {
      expect(screen.getByRole('link', { name })).toBeTruthy();
    }
    expect(screen.queryByText('nav.workspaces')).toBeNull();
  });

  it('puts both settings and the shortcuts button in the footer', async () => {
    renderSidebar();
    await screen.findByRole('heading', { name: 'Acme' });

    expect(screen.getByRole('link', { name: 'nav.workspaceSettings' }).getAttribute('href')).toBe(
      '/workspaces/ws-1/settings',
    );
    expect(screen.getByRole('link', { name: 'nav.settings' }).getAttribute('href')).toBe(
      '/settings/notifications',
    );

    const received = vi.fn();
    window.addEventListener(OPEN_SHORTCUTS_HELP_EVENT, received);
    await userEvent.setup().click(screen.getByRole('button', { name: 'nav.shortcuts' }));
    window.removeEventListener(OPEN_SHORTCUTS_HELP_EVENT, received);
    expect(received).toHaveBeenCalledTimes(1);
  });

  it('adds no second <aside>', async () => {
    const container = renderSidebar();
    await screen.findByRole('heading', { name: 'Acme' });

    expect(container.querySelectorAll('aside')).toHaveLength(1);
  });
});

describe('sidebar with no workspace', () => {
  beforeEach(() => {
    mocks.workspaces = [];
    clearActiveWorkspaceId();
  });

  it('offers to create one and hides workspace settings', async () => {
    renderSidebar();

    const cta = await screen.findByRole('link', { name: 'nav.noWorkspace.cta' });
    expect(cta.getAttribute('href')).toBe('/setup');
    expect(screen.queryByText('nav.workspaceSettings')).toBeNull();
    expect(screen.queryByRole('link', { name: 'nav.projects' })).toBeNull();
  });
});

describe('new project button', () => {
  it('shows for an admin', async () => {
    mocks.role = 'admin';
    renderSidebar();
    await screen.findByRole('heading', { name: 'Acme' });

    expect(screen.getByRole('button', { name: 'projects.new' })).toBeTruthy();
  });

  it.each(['member', 'guest', null])('is absent when the role is %s', async (role) => {
    mocks.role = role;
    renderSidebar();
    await screen.findByRole('heading', { name: 'Acme' });
    expect(screen.queryByRole('button', { name: 'projects.new' })).toBeNull();
  });
});

describe('favorites placement', () => {
  it('renders the favorites section after the More toggle', async () => {
    renderSidebar();
    const toggle = await screen.findByRole('button', { name: 'nav.more' });
    const favorites = await screen.findByText('favorites.sidebar_title');
    expect(
      toggle.compareDocumentPosition(favorites) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

describe('More disclosure', () => {
  it('starts closed and records the user toggling it', async () => {
    const user = userEvent.setup();
    renderSidebar();
    const toggle = await screen.findByRole('button', { name: 'nav.more' });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByText('nav.history')).toBeNull();

    await user.click(toggle);

    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByText('nav.history')).toBeTruthy();
    expect(window.localStorage.getItem('nf.sidebar-more-open')).toBe('1');
  });

  it('opens at first mount when a route under it is active', async () => {
    mocks.pathname = '/workspaces/ws-1/activity';
    renderSidebar();

    const toggle = await screen.findByRole('button', { name: 'nav.more' });
    await waitFor(() => {
      expect(toggle.getAttribute('aria-expanded')).toBe('true');
    });
  });

  it('opens at first mount when the stored preference says so', async () => {
    window.localStorage.setItem('nf.sidebar-more-open', '1');
    renderSidebar();

    const toggle = await screen.findByRole('button', { name: 'nav.more' });
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
  });

  describe('scrolling the toggle into view', () => {
    const scrollIntoView = vi.fn();

    beforeEach(() => {
      scrollIntoView.mockClear();
      Element.prototype.scrollIntoView = scrollIntoView;
      vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
        cb(0);
        return 0;
      });
    });

    afterEach(() => {
      vi.unstubAllGlobals();
    });

    it('scrolls the toggle to the top when the user opens it', async () => {
      const user = userEvent.setup();
      renderSidebar();
      const toggle = await screen.findByRole('button', { name: 'nav.more' });

      await user.click(toggle);

      expect(scrollIntoView).toHaveBeenCalledTimes(1);
      expect(scrollIntoView.mock.contexts[0]).toBe(toggle);
      expect(scrollIntoView.mock.calls[0]?.[0]).toMatchObject({ block: 'start' });
    });

    it('does not scroll when the user closes it', async () => {
      window.localStorage.setItem('nf.sidebar-more-open', '1');
      const user = userEvent.setup();
      renderSidebar();
      const toggle = await screen.findByRole('button', { name: 'nav.more' });

      await user.click(toggle);

      expect(toggle.getAttribute('aria-expanded')).toBe('false');
      expect(scrollIntoView).not.toHaveBeenCalled();
    });

    it('does not scroll when an active child route opens it at mount', async () => {
      mocks.pathname = '/workspaces/ws-1/activity';
      renderSidebar();
      const toggle = await screen.findByRole('button', { name: 'nav.more' });
      await waitFor(() => {
        expect(toggle.getAttribute('aria-expanded')).toBe('true');
      });

      expect(scrollIntoView).not.toHaveBeenCalled();
    });
  });
});

describe('history entry', () => {
  it.each(['/workspaces/ws-1/timeline', '/workspaces/ws-1/activity'])(
    'is the current page on %s',
    async (path) => {
      mocks.pathname = path;
      window.localStorage.setItem('nf.sidebar-more-open', '1');
      renderSidebar();

      const link = await screen.findByRole('link', { name: 'nav.history' });
      expect(link.getAttribute('aria-current')).toBe('page');
    },
  );

  it('is not current elsewhere', async () => {
    mocks.pathname = '/workspaces/ws-1/members';
    window.localStorage.setItem('nf.sidebar-more-open', '1');
    renderSidebar();

    const link = await screen.findByRole('link', { name: 'nav.history' });
    expect(link.getAttribute('aria-current')).toBeNull();
  });
});

describe('project entries', () => {
  beforeEach(() => {
    mocks.projects = [{ id: 'p1', name: 'Platform', isArchived: false }];
    mocks.favorites = [{ id: 'f1', targetType: 'project', targetId: 'p2', title: 'Roadmap' }];
  });

  it('links the project row and the favorite to the task list', async () => {
    renderSidebar();
    const row = await screen.findByRole('link', { name: 'Platform' });
    const favorite = await screen.findByRole('link', { name: 'Roadmap' });

    expect(row.getAttribute('href')).toBe('/workspaces/ws-1/projects/p1/tasks');
    expect(favorite.getAttribute('href')).toBe('/workspaces/ws-1/projects/p2/tasks');
  });

  it('keeps the project row active on the other project tabs', async () => {
    mocks.pathname = '/workspaces/ws-1/projects/p1/gantt';
    renderSidebar();
    const row = await screen.findByRole('link', { name: 'Platform' });

    expect(row.className).toContain('itemActive');
  });

  it('leaves the project row inactive elsewhere', async () => {
    renderSidebar();
    const row = await screen.findByRole('link', { name: 'Platform' });

    expect(row.className).not.toContain('itemActive');
  });
});
