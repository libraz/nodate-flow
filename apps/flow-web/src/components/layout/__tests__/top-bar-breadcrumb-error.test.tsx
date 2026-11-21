/**
 * The breadcrumb is decorative: a rejected project query (403) with the
 * shared `throwOnError: true` client must be contained by the top bar.
 */

import { QueryClient, QueryClientProvider, useSuspenseQuery } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { ApiError } from '../../../lib/api-error';

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/workspaces/w1/projects/p1/settings' } }),
  Link: ({ children }: { children: ReactNode }) => <a href="/">{children}</a>,
}));

vi.mock('../../../features/ai-providers/cost-meter', () => ({ default: () => null }));
vi.mock('../../../features/notifications/notification-bell', () => ({ default: () => null }));
vi.mock('../command-palette', () => ({ default: () => null }));

vi.mock('../use-top-bar-breadcrumb', () => ({
  PROJECT_VIEW_KEY: {},
  useTopBarBreadcrumb: () => ({ workspaceId: 'w1', projectId: 'p1', view: null }),
}));

vi.mock('../../../features/workspaces/api', () => ({
  useWorkspaceQuery: () => ({ data: { name: 'WS' } }),
}));

vi.mock('../../../features/projects/api', () => ({
  useProjectQuery: () =>
    useSuspenseQuery({
      queryKey: ['project', 'p1'],
      queryFn: async () => {
        throw new ApiError('FORBIDDEN', 'forbidden', 403);
      },
      retry: false,
    }),
}));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => null,
  isWorkspaceAdmin: () => false,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import TopBar from '../top-bar';

describe('TopBar breadcrumb failure', () => {
  it('renders the bar without throwing when the project query is forbidden', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, throwOnError: true } },
    });
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <QueryClientProvider client={client}>
        <TopBar />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole('button', { name: /topbar\.search\.placeholder/ })).toBeTruthy();
    await vi.waitFor(() => {
      expect(client.getQueryState(['project', 'p1'])?.status).toBe('error');
    });
    expect(screen.getByRole('banner')).toBeTruthy();
  });
});
