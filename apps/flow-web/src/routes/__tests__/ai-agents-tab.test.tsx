/**
 * Agents tab copy and controls by role and model availability: the empty
 * state and the create form pick their wording from whether any model
 * exists, guests get a read-only list.
 */

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  role: 'admin' as string | null,
  agents: [] as Array<Record<string, unknown>>,
  models: [] as Array<Record<string, unknown>>,
}));

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: ReactNode; to: string; params: { id: string } }) => (
    <a href={to.replace('$id', params.id)}>{children}</a>
  ),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => mocks.role,
  isWorkspaceAdmin: (role: string | null) => role === 'owner' || role === 'admin',
}));

vi.mock('../../components/section-header', () => ({
  default: ({ title, actions }: { title: ReactNode; actions?: ReactNode }) => (
    <header>
      <h2>{title}</h2>
      {actions}
    </header>
  ),
}));

vi.mock('../../features/ai-providers/agent-model-select', () => ({
  default: () => <select aria-label="model-select" />,
}));

vi.mock('../../features/ai-providers/agents-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../features/ai-providers/agents-api')>();
  const mutation = { mutateAsync: vi.fn(), isPending: false, variables: undefined };
  return {
    ...actual,
    useAgentsQuery: () => ({ data: { total: mocks.agents.length, agents: mocks.agents } }),
    useModelsQuery: () => ({ data: { total: mocks.models.length, models: mocks.models } }),
    useCreateAgent: () => mutation,
    usePauseAgent: () => mutation,
    useTriggerAgent: () => mutation,
    useUpdateAgentEventTriggers: () => mutation,
    useUpdateAgentSchedule: () => mutation,
  };
});

import AgentsPage from '../../features/ai-providers/agents-page';

const CONNECTION = '/workspaces/ws-1/settings/ai/connection';

const AGENT = {
  id: 'a-1',
  name: 'Planner',
  systemPrompt: 'p',
  modelId: 'm-1',
  modelName: 'Model One',
  scheduleKind: 'on_event',
  paused: false,
  eventTriggerTypes: [],
  createdAt: 0,
};

const MODEL = { id: 'm-1', displayName: 'Model One', providerName: 'Acme' };

function renderPage(): void {
  render(<AgentsPage workspaceId="ws-1" />);
}

function createButtons(): HTMLElement[] {
  return screen.queryAllByRole('button', { name: 'agents.create.open' });
}

beforeEach(() => {
  mocks.role = 'admin';
  mocks.agents = [];
  mocks.models = [];
});

describe('Agents tab empty state', () => {
  it.each(['admin', 'member'])('offers creation to %s when a model exists', (role) => {
    mocks.role = role;
    mocks.models = [MODEL];
    renderPage();
    expect(screen.getByText('agents.empty_ready')).toBeTruthy();
    expect(createButtons().length).toBeGreaterThan(0);
  });

  it('shows a guest the same text but no create button', () => {
    mocks.role = 'guest';
    mocks.models = [MODEL];
    renderPage();
    expect(screen.getByText('agents.empty_ready')).toBeTruthy();
    expect(createButtons()).toHaveLength(0);
  });

  it('points an admin without models at the Connection tab', () => {
    renderPage();
    expect(screen.getByText('agents.empty')).toBeTruthy();
    const link = screen.getByRole('link', { name: 'ai_page.tabs.connection' });
    expect(link.getAttribute('href')).toBe(CONNECTION);
    expect(screen.queryByText('agents.empty_member')).toBeNull();
  });

  it.each(['member', 'guest'])('gives %s without models the member wording and no link', (role) => {
    mocks.role = role;
    renderPage();
    expect(screen.getByText('agents.empty_member')).toBeTruthy();
    expect(screen.queryByRole('link')).toBeNull();
    expect(screen.queryByText('agents.empty')).toBeNull();
  });
});

describe('Agents tab create form without models', () => {
  it('links an admin to Connection', async () => {
    renderPage();
    await userEvent.click(createButtons()[0] as HTMLElement);
    expect(screen.getByText('agents.create.noModels')).toBeTruthy();
    expect(
      screen
        .getAllByRole('link', { name: 'ai_page.tabs.connection' })
        .map((a) => a.getAttribute('href')),
    ).toContain(CONNECTION);
  });

  it('tells a member to ask an admin, with no link', async () => {
    mocks.role = 'member';
    renderPage();
    // The empty state has no create action without models; the header button opens the form.
    await userEvent.click(createButtons()[0] as HTMLElement);
    expect(screen.getByText('agents.create.noModels_member')).toBeTruthy();
    expect(screen.queryByText('agents.create.noModels')).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
  });
});

describe('Agents tab controls', () => {
  beforeEach(() => {
    mocks.agents = [AGENT];
    mocks.models = [MODEL];
  });

  it.each(['admin', 'member'])('gives %s the write controls', (role) => {
    mocks.role = role;
    renderPage();
    expect(screen.getByRole('button', { name: 'agents.pause' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'agents.trigger.label' })).toBeTruthy();
    expect(screen.getByLabelText('model-select')).toBeTruthy();
    expect(createButtons()).toHaveLength(1);
  });

  it('shows a guest the list read-only', () => {
    mocks.role = 'guest';
    renderPage();
    expect(screen.getByText('Planner')).toBeTruthy();
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByLabelText('agents.eventTriggers.label')).toBeNull();
  });
});
