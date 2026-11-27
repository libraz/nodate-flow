/**
 * AI settings layout: Connection and Automation tabs are admin-only, and the
 * index route sends each role to the first tab it can use.
 */

import { render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  role: null as string | null,
  isPending: false,
}));

vi.mock('@tanstack/react-router', () => ({
  createLazyFileRoute: () => (options: unknown) => ({ options }),
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
  Link: ({ children, to, params }: { children: ReactNode; to: string; params: { id: string } }) => (
    <a href={to.replace('$id', params.id)}>{children}</a>
  ),
  Navigate: ({ to, replace }: { to: string; replace?: boolean }) => (
    <div data-testid="navigate" data-to={to} data-replace={String(replace === true)} />
  ),
  Outlet: () => <div data-testid="outlet" />,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  useCurrentWorkspaceRoleState: () => ({ role: mocks.role, isPending: mocks.isPending }),
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('../../components/page-header', () => ({
  default: ({
    title,
    description,
    help,
  }: {
    title: ReactNode;
    description: ReactNode;
    help: string;
  }) => (
    <header data-help={help}>
      <h1>{title}</h1>
      <p>{description}</p>
    </header>
  ),
}));

vi.mock('../../features/ai-providers/ai-status-card', () => ({
  default: () => <section data-testid="status-card" />,
}));

import { Route as IndexRoute } from '../_authenticated.workspaces.$id.settings.ai.index.lazy';
import { Route as LayoutRoute } from '../_authenticated.workspaces.$id.settings.ai.lazy';

function componentOf(route: unknown): () => ReactNode {
  return (route as { options: { component: () => ReactNode } }).options.component;
}

function renderLayout(): void {
  const Component = componentOf(LayoutRoute);
  render(<Component />);
}

function tabHrefs(): string[] {
  return within(screen.getByRole('navigation', { name: 'ai_page.tabs_label' }))
    .getAllByRole('link')
    .map((a) => a.getAttribute('href') ?? '');
}

beforeEach(() => {
  mocks.role = null;
  mocks.isPending = false;
});

describe('AI settings layout', () => {
  it.each(['owner', 'admin'])('shows %s all four tabs in order', (role) => {
    mocks.role = role;
    renderLayout();
    expect(tabHrefs()).toEqual([
      '/workspaces/ws-1/settings/ai/connection',
      '/workspaces/ws-1/settings/ai/automation',
      '/workspaces/ws-1/settings/ai/agents',
      '/workspaces/ws-1/settings/ai/usage',
    ]);
  });

  it.each(['member', 'guest', null])('shows role %s only Agents and Usage', (role) => {
    mocks.role = role;
    renderLayout();
    expect(tabHrefs()).toEqual([
      '/workspaces/ws-1/settings/ai/agents',
      '/workspaces/ws-1/settings/ai/usage',
    ]);
    expect(screen.queryByText('ai_page.tabs.connection')).toBeNull();
    expect(screen.queryByText('ai_page.tabs.automation')).toBeNull();
  });

  it('stacks header, status card, agents note, tabs and outlet in that order', () => {
    mocks.role = 'admin';
    renderLayout();
    const header = screen.getByRole('heading', { level: 1, name: 'ai_page.title' });
    expect(header.parentElement?.getAttribute('data-help')).toBe('workspace-settings-ai');
    const ordered: Node[] = [
      header,
      screen.getByTestId('status-card'),
      screen.getByText('ai_page.agents_note'),
      screen.getByRole('navigation', { name: 'ai_page.tabs_label' }),
      screen.getByTestId('outlet'),
    ];
    for (let i = 1; i < ordered.length; i += 1) {
      const prev = ordered[i - 1] as Node;
      const next = ordered[i] as Node;
      expect(prev.compareDocumentPosition(next) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
  });
});

describe('AI settings index', () => {
  function renderIndex(): void {
    const Component = componentOf(IndexRoute);
    render(<Component />);
  }

  it.each(['owner', 'admin'])('sends %s to connection', (role) => {
    mocks.role = role;
    renderIndex();
    const nav = screen.getByTestId('navigate');
    expect(nav.getAttribute('data-to')).toBe('/workspaces/$id/settings/ai/connection');
    expect(nav.getAttribute('data-replace')).toBe('true');
  });

  it.each(['member', 'guest', null])('sends role %s (including unknown) to agents', (role) => {
    mocks.role = role;
    renderIndex();
    expect(screen.getByTestId('navigate').getAttribute('data-to')).toBe(
      '/workspaces/$id/settings/ai/agents',
    );
  });

  it('waits for the role lookup before redirecting', () => {
    mocks.isPending = true;
    renderIndex();
    expect(screen.queryByTestId('navigate')).toBeNull();
  });
});
