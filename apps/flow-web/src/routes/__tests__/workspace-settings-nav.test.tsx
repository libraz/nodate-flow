/**
 * The workspace settings nav lists only what the caller's role can open:
 * admin-floor sections need owner/admin (and stay hidden while the role is
 * unknown), public shares are hidden from guests.
 */

import { render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ role: null as string | null }));

vi.mock('@tanstack/react-router', () => ({
  createLazyFileRoute: () => (options: unknown) => ({ options }),
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/workspaces/ws-1/settings/general' } }),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  Outlet: () => null,
}));

vi.mock('../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import { Route } from '../_authenticated.workspaces.$id.settings.lazy';

function renderNav(): void {
  const Component = (Route as unknown as { options: { component: () => ReactNode } }).options
    .component;
  render(<Component />);
}

function linkKeys(): string[] {
  return within(screen.getByRole('navigation'))
    .queryAllByRole('link')
    .map((a) => a.textContent ?? '');
}

const ADMIN_ONLY = ['nav.webhooks', 'nav.audit_log'];
const REMOVED_AI = [
  'nav.ai_providers',
  'nav.ai_agents',
  'nav.auto_actions',
  'nav.ai_activity',
  'nav.ai_metrics',
];

beforeEach(() => {
  mocks.role = null;
});

describe('workspace settings nav', () => {
  it('shows member exactly 6 items, none of them admin-floor', () => {
    mocks.role = 'member';
    renderNav();
    const keys = linkKeys();
    expect(keys).toHaveLength(6);
    expect(keys).toContain('nav.ai');
    for (const key of REMOVED_AI) expect(keys).not.toContain(key);
    for (const key of ADMIN_ONLY) expect(keys).not.toContain(key);
    expect(keys).toContain('nav.public_shares');
  });

  it('shows guest 5 items without public shares', () => {
    mocks.role = 'guest';
    renderNav();
    const keys = linkKeys();
    expect(keys).toHaveLength(5);
    expect(keys).toContain('nav.ai');
    expect(keys).not.toContain('nav.public_shares');
  });

  it.each(['owner', 'admin'])('shows %s the admin-floor items', (role) => {
    mocks.role = role;
    renderNav();
    const keys = linkKeys();
    for (const key of ADMIN_ONLY.filter((k) => k !== 'nav.audit_log')) {
      expect(keys).toContain(key);
    }
    expect(keys).toContain('nav.ai');
  });

  it('hides admin-floor items while the role is unknown', () => {
    mocks.role = null;
    renderNav();
    const keys = linkKeys();
    expect(keys).toContain('nav.ai');
    for (const key of ADMIN_ONLY) expect(keys).not.toContain(key);
    expect(keys).not.toContain('nav.public_shares');
  });

  it('lists the single AI item last in the workspace group and has no AI group', () => {
    mocks.role = 'owner';
    renderNav();
    expect(screen.queryByText('nav_group.ai')).toBeNull();
    const keys = linkKeys();
    expect(keys.indexOf('nav.ai')).toBe(keys.indexOf('nav.public_shares') + 1);
    expect(keys.indexOf('nav.ai')).toBeLessThan(keys.indexOf('nav.mcp_tokens'));
  });

  it('orders the groups workspace, integrations, reports', () => {
    mocks.role = 'owner';
    renderNav();
    const labels = ['workspace', 'integrations', 'reports'].map((g) =>
      screen.getByText(`nav_group.${g}`),
    );
    for (let i = 1; i < labels.length; i += 1) {
      const prev = labels[i - 1] as Node;
      const next = labels[i] as Node;
      expect(prev.compareDocumentPosition(next) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
  });
});
