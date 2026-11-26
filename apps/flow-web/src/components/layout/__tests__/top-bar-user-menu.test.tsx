/**
 * The user menu sends profile and security to accounts-web and keeps
 * notification settings inside flow-web.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/today' } }),
}));

vi.mock('../../../features/notifications/notification-bell', () => ({ default: () => null }));
vi.mock('../top-bar-breadcrumb', () => ({ default: () => null }));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => null,
  isWorkspaceAdmin: () => false,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { accountsWebUrl } from '../../../lib/accounts-web-url';
import TopBar from '../top-bar';

describe('TopBar user menu', () => {
  it('links the account entry to accounts-web as a real anchor', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <TopBar />
      </QueryClientProvider>,
    );
    await userEvent.setup().click(screen.getByRole('button', { name: /topbar\.user_menu\.open/ }));

    const account = screen.getByRole('menuitem', { name: 'topbar.user_menu.account' });
    expect(account.tagName).toBe('A');
    expect(account.getAttribute('href')).toBe(accountsWebUrl('/profile'));
    expect(
      screen.getByRole('menuitem', { name: 'topbar.user_menu.notification_settings' }).tagName,
    ).toBe('BUTTON');
  });
});
