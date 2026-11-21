/**
 * The AI cost meter belongs to the people who can act on spend: it shows
 * only for an owner/admin of the current workspace. The top bar no longer
 * carries a workspace switcher, so there is no combobox either.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  role: null as string | null,
}));

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/today' } }),
  Link: ({ children }: { children: ReactNode }) => <a href="/">{children}</a>,
}));

vi.mock('../../../features/ai-providers/cost-meter', () => ({
  default: () => <div data-testid="ai-cost-meter" />,
}));

vi.mock('../../../features/notifications/notification-bell', () => ({
  default: () => <div data-testid="notification-bell" />,
}));

vi.mock('../top-bar-breadcrumb', () => ({ default: () => <nav aria-label="breadcrumb" /> }));

vi.mock('../command-palette', () => ({ default: () => null }));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import TopBar from '../top-bar';

function renderTopBar(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <TopBar />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mocks.role = null;
});

describe('TopBar AI cost meter', () => {
  it.each(['owner', 'admin'])('shows for %s', (role) => {
    mocks.role = role;
    renderTopBar();

    expect(screen.getByTestId('ai-cost-meter')).toBeTruthy();
  });

  it.each(['member', 'guest'])('is hidden for %s', (role) => {
    mocks.role = role;
    renderTopBar();

    expect(screen.queryByTestId('ai-cost-meter')).toBeNull();
  });

  it('is hidden while the role is unknown', () => {
    renderTopBar();

    expect(screen.queryByTestId('ai-cost-meter')).toBeNull();
  });

  it('has no workspace switcher control', () => {
    mocks.role = 'admin';
    renderTopBar();

    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByRole('button', { name: /nav\.switchWorkspace/ })).toBeNull();
  });
});
