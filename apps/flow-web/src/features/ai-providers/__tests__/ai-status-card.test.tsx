/**
 * The status card's three rows: state text, badge, and the one action each
 * state offers to an admin versus everyone else.
 */

import { render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

type Provider = { state: string; name?: string; model?: string };
type Status = {
  provider: Provider;
  embedding: { status: string };
  autoActions: { enabled: boolean; intervalMinutes: number };
};

const mocks = vi.hoisted(() => ({
  role: 'admin' as string | null,
  query: {} as Record<string, unknown>,
}));

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: ReactNode; to: string; params: { id: string } }) => (
    <a href={to.replace('$id', params.id)}>{children}</a>
  ),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('../ai-status-api', () => ({
  useAiStatusQuery: () => mocks.query,
}));

import AiStatusCard from '../ai-status-card';

const CONNECTION = '/workspaces/ws-1/settings/ai/connection';
const AUTOMATION = '/workspaces/ws-1/settings/ai/automation';

function status(over: Partial<Status> = {}): Status {
  return {
    provider: { state: 'ready', name: 'Acme', model: 'm-1' },
    embedding: { status: 'enabled' },
    autoActions: { enabled: true, intervalMinutes: 15 },
    ...over,
  };
}

function setStatus(s: Status): void {
  mocks.query = { data: s, isError: false };
}

function rows(): HTMLElement[] {
  return within(screen.getByRole('region', { name: 'ai_page.status.title' })).getAllByRole(
    'listitem',
  );
}

function renderCard(): void {
  render(<AiStatusCard workspaceId="ws-1" />);
}

beforeEach(() => {
  mocks.role = 'admin';
  setStatus(status());
});

describe('AiStatusCard', () => {
  it('renders a named region with three rows', () => {
    renderCard();
    expect(rows()).toHaveLength(3);
  });

  it('shows the ready provider and model with no fix link', () => {
    renderCard();
    const [features] = rows() as [HTMLElement];
    expect(within(features).getByText('ai_page.status.features.ready')).toBeTruthy();
    expect(within(features).queryByRole('link')).toBeNull();
  });

  it('falls back to the no-model wording when the model is missing', () => {
    setStatus(status({ provider: { state: 'ready', name: 'mock' } }));
    renderCard();
    expect(screen.getByText('ai_page.status.features.ready_no_model')).toBeTruthy();
  });

  it.each([
    ['none', 'ai_page.status.features.none'],
    ['no_default_model', 'ai_page.status.features.no_default_model'],
  ])('links an admin to Connection when the provider is %s', (state, text) => {
    setStatus(status({ provider: { state, name: 'Acme' } }));
    renderCard();
    const [features] = rows() as [HTMLElement];
    expect(within(features).getByText(text)).toBeTruthy();
    const link = within(features).getByRole('link', { name: 'ai_page.status.fix_link' });
    expect(link.getAttribute('href')).toBe(CONNECTION);
  });

  it.each(['member', 'guest'])('asks %s to contact an admin instead of linking', (role) => {
    mocks.role = role;
    setStatus(status({ provider: { state: 'none' } }));
    renderCard();
    const [features] = rows() as [HTMLElement];
    expect(within(features).getByText('ai_page.status.ask_admin')).toBeTruthy();
    expect(within(features).queryByRole('link')).toBeNull();
  });

  it('offers nothing when AI is unavailable on the server', () => {
    setStatus(status({ provider: { state: 'unavailable' } }));
    renderCard();
    const [features] = rows() as [HTMLElement];
    expect(within(features).getByText('ai_page.status.features.unavailable')).toBeTruthy();
    expect(within(features).queryByRole('link')).toBeNull();
    expect(within(features).queryByText('ai_page.status.ask_admin')).toBeNull();
  });

  it('shows embeddings enabled without an action', () => {
    renderCard();
    const embedding = rows()[1] as HTMLElement;
    expect(within(embedding).getByText('ai_page.status.embedding.enabled')).toBeTruthy();
    expect(within(embedding).queryByRole('link')).toBeNull();
  });

  it('links an admin to Connection when embeddings are disabled', () => {
    setStatus(status({ embedding: { status: 'disabled' } }));
    renderCard();
    const embedding = rows()[1] as HTMLElement;
    expect(within(embedding).getByText('ai_page.status.embedding.disabled')).toBeTruthy();
    expect(within(embedding).getByRole('link').getAttribute('href')).toBe(CONNECTION);
  });

  it('asks a member to contact an admin when embeddings are disabled', () => {
    mocks.role = 'member';
    setStatus(status({ embedding: { status: 'disabled' } }));
    renderCard();
    const embedding = rows()[1] as HTMLElement;
    expect(within(embedding).getByText('ai_page.status.ask_admin')).toBeTruthy();
    expect(within(embedding).queryByRole('link')).toBeNull();
  });

  it('offers nothing for embeddings the server has not configured, to anyone', () => {
    setStatus(status({ embedding: { status: 'unconfigured' } }));
    renderCard();
    const embedding = rows()[1] as HTMLElement;
    expect(within(embedding).getByText('ai_page.status.embedding.unconfigured')).toBeTruthy();
    expect(within(embedding).queryByRole('link')).toBeNull();
    expect(within(embedding).queryByText('ai_page.status.ask_admin')).toBeNull();
  });

  it('shows auto-actions on and always links an admin to Automation', () => {
    renderCard();
    const auto = rows()[2] as HTMLElement;
    expect(within(auto).getByText('ai_page.status.auto_actions.on')).toBeTruthy();
    expect(within(auto).getByRole('link').getAttribute('href')).toBe(AUTOMATION);
  });

  it.each([
    ['disabled', { enabled: false, intervalMinutes: 15 }],
    ['a zero interval', { enabled: true, intervalMinutes: 0 }],
  ])('shows auto-actions off for %s, still linked for an admin', (_name, autoActions) => {
    setStatus(status({ autoActions }));
    renderCard();
    const auto = rows()[2] as HTMLElement;
    expect(within(auto).getByText('ai_page.status.auto_actions.off')).toBeTruthy();
    expect(within(auto).getByRole('link').getAttribute('href')).toBe(AUTOMATION);
  });

  it.each(['member', 'guest'])('gives %s no auto-actions action at all', (role) => {
    mocks.role = role;
    renderCard();
    const auto = rows()[2] as HTMLElement;
    expect(within(auto).queryByRole('link')).toBeNull();
    expect(within(auto).queryByText('ai_page.status.ask_admin')).toBeNull();
  });

  it('shows a failure message and no rows when the query errors', () => {
    mocks.query = { data: undefined, isError: true };
    renderCard();
    expect(screen.getByText('ai_page.status.load_failed')).toBeTruthy();
    expect(screen.queryAllByRole('listitem')).toHaveLength(0);
  });

  it('shows no rows while loading', () => {
    mocks.query = { data: undefined, isError: false };
    renderCard();
    expect(screen.queryAllByRole('listitem')).toHaveLength(0);
    expect(screen.queryByText('ai_page.status.load_failed')).toBeNull();
  });
});
