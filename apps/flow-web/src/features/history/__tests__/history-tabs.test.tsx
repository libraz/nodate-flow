import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ pathname: '/workspaces/ws-1/timeline' }));

vi.mock('@tanstack/react-router', () => ({
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: mocks.pathname } }),
  Link: ({
    to,
    params,
    children,
    ...rest
  }: {
    to: string;
    params: Record<string, string>;
    children: ReactNode;
  } & Record<string, unknown>) => (
    <a href={to.replace('$id', params.id ?? '')} {...rest}>
      {children}
    </a>
  ),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import HistoryTabs from '../history-tabs';

beforeEach(() => {
  mocks.pathname = '/workspaces/ws-1/timeline';
});

describe('HistoryTabs', () => {
  it('marks the timeline tab on the timeline page', () => {
    render(<HistoryTabs workspaceId="ws-1" />);
    expect(screen.getByRole('link', { name: 'nav.timeline' }).getAttribute('aria-current')).toBe(
      'page',
    );
    expect(
      screen.getByRole('link', { name: 'nav.activity' }).getAttribute('aria-current'),
    ).toBeNull();
  });

  it('marks the activity tab on the activity page', () => {
    mocks.pathname = '/workspaces/ws-1/activity';
    render(<HistoryTabs workspaceId="ws-1" />);
    expect(screen.getByRole('link', { name: 'nav.activity' }).getAttribute('aria-current')).toBe(
      'page',
    );
    expect(
      screen.getByRole('link', { name: 'nav.timeline' }).getAttribute('aria-current'),
    ).toBeNull();
  });
});
