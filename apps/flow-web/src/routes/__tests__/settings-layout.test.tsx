/**
 * The personal settings sub-nav leads with an external link to the
 * accounts-web profile page and offers only notifications and integrations
 * inside flow-web.
 */

import { render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@tanstack/react-router', () => ({
  createLazyFileRoute: () => (options: unknown) => ({ options }),
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/settings/notifications' } }),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  Outlet: () => null,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { accountsWebUrl } from '../../lib/accounts-web-url';
import { Route } from '../_authenticated.settings.lazy';

function renderLayout(): void {
  const Component = (Route as unknown as { options: { component: () => ReactNode } }).options
    .component;
  render(<Component />);
}

describe('personal settings layout', () => {
  it('leads with an accounts-web profile link', () => {
    renderLayout();
    const links = within(screen.getByRole('navigation')).getAllByRole('link');
    expect(links[0]?.textContent).toBe('nav.account');
    expect(links[0]?.getAttribute('href')).toBe(accountsWebUrl('/profile'));
  });

  it('has no profile or security routes of its own', () => {
    renderLayout();
    const hrefs = within(screen.getByRole('navigation'))
      .getAllByRole('link')
      .map((a) => a.getAttribute('href'));
    expect(hrefs.slice(1)).toEqual(['/settings/notifications', '/settings/integrations']);
  });

  it('explains where profile and security live', () => {
    renderLayout();
    expect(screen.getByText('nav.account_hint')).toBeTruthy();
  });
});
