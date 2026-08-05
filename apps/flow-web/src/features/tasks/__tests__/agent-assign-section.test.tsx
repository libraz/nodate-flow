/**
 * Assigning an AI agent to a task from the task detail page.
 *
 * The backend has accepted `POST /tasks/{id}/agents` all along and the
 * web client never called it: the agent panel only rendered once an
 * agent was already attached, and the only wired mutation was removal.
 * The result was a one-way door — you could take an agent off a task
 * from the UI but never put one on.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  del: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  // openapi-fetch keys its client by HTTP method, hence the caps.
  sdk: { GET: mocks.get, POST: mocks.post, DELETE: mocks.del },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, vars?: Record<string, unknown>) =>
      vars?.name ? `${key}:${String(vars.name)}` : key,
  }),
}));

import AgentAssignSection from '../agent-panel/agent-assign-section';

/* ── fixtures ─────────────────────────────────────────────────── */

const AGENTS = [
  { id: 'agent-1', name: 'Planner', paused: false },
  { id: 'agent-2', name: 'Reviewer', paused: true },
];

/** Route each mocked GET by path so both suspense queries resolve. */
function serveAssigned(assigned: Array<{ id: string; agentId: string; agentName: string }>): void {
  mocks.get.mockImplementation(async (path: string): Promise<unknown> => {
    if (path === '/tasks/{id}/agents') {
      return { data: { agents: assigned, total: assigned.length }, error: null };
    }
    if (path === '/workspaces/{wsId}/ai/agents') {
      return { data: { agents: AGENTS, total: AGENTS.length }, error: null };
    }
    return { data: null, error: { type: 'INTERNAL.UNEXPECTED', detail: `unmocked ${path}` } };
  });
}

function renderSection(): void {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={client}>
        <Suspense fallback={null}>{children}</Suspense>
      </QueryClientProvider>
    );
  }
  render(
    <Wrapper>
      <AgentAssignSection taskId="task-1" workspaceId="ws-1" />
    </Wrapper>,
  );
}

/**
 * Reveal the agent list. The picker is a WAI-ARIA combobox — a text
 * input plus a portal-rendered listbox — so it is driven by clicking,
 * not by `selectOptions`.
 */
async function openPicker(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(await screen.findByRole('button', { name: 'task_detail.agent.assign.add' }));
  await user.click(screen.getByRole('combobox', { name: 'task_detail.agent.assign.add' }));
  await waitFor(() => {
    expect(screen.getByRole('listbox')).toBeDefined();
  });
}

/** Open the picker and choose the option with the given visible label. */
async function pickAgent(user: ReturnType<typeof userEvent.setup>, label: string): Promise<void> {
  await openPicker(user);
  await user.click(screen.getByRole('option', { name: label }));
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.post.mockReset().mockResolvedValue({
    data: { id: 'actor-9', agentId: 'agent-1', agentName: 'Planner', role: 'assignee' },
    error: null,
  });
  mocks.del.mockReset().mockResolvedValue({ error: null });
  mocks.toastShow.mockReset();
});

describe('AgentAssignSection', () => {
  it('offers a picker when no agent is attached yet', async () => {
    serveAssigned([]);
    renderSection();

    expect(await screen.findByText('task_detail.agent.assign.empty')).toBeDefined();
    expect(screen.getByRole('button', { name: 'task_detail.agent.assign.add' })).toBeDefined();
  });

  it('posts the chosen agent to the task', async () => {
    const user = userEvent.setup();
    serveAssigned([]);
    renderSection();

    await pickAgent(user, 'Planner');

    expect(mocks.post).toHaveBeenCalledTimes(1);
    const [path, init] = mocks.post.mock.calls[0] as [string, { params: unknown; body: unknown }];
    expect(path).toBe('/tasks/{id}/agents');
    expect(init.params).toEqual({ path: { id: 'task-1' } });
    expect(init.body).toEqual({ agentId: 'agent-1', role: 'assignee' });
  });

  it('lists an already-attached agent and does not offer it again', async () => {
    const user = userEvent.setup();
    serveAssigned([{ id: 'actor-1', agentId: 'agent-1', agentName: 'Planner' }]);
    renderSection();

    expect(await screen.findByText('Planner')).toBeDefined();

    await openPicker(user);
    expect(screen.queryByRole('option', { name: 'Planner' })).toBeNull();
    expect(
      screen.getByRole('option', { name: 'task_detail.agent.assign.paused_option:Reviewer' }),
    ).toBeDefined();
  });

  it('marks a paused agent so an assignment that will not run says so', async () => {
    const user = userEvent.setup();
    serveAssigned([]);
    renderSection();

    await openPicker(user);

    expect(
      screen.getByRole('option', { name: 'task_detail.agent.assign.paused_option:Reviewer' }),
    ).toBeDefined();
  });

  it('unassigns through the shared actor endpoint', async () => {
    const user = userEvent.setup();
    serveAssigned([{ id: 'actor-1', agentId: 'agent-1', agentName: 'Planner' }]);
    renderSection();

    await user.click(
      await screen.findByRole('button', { name: 'task_detail.agent.assign.remove:Planner' }),
    );

    expect(mocks.del).toHaveBeenCalledTimes(1);
    const [path, init] = mocks.del.mock.calls[0] as [string, { params: unknown }];
    expect(path).toBe('/tasks/{id}/actors/{actorId}');
    expect(init.params).toEqual({ path: { id: 'task-1', actorId: 'actor-1' } });
  });

  it('surfaces a failed assignment instead of silently doing nothing', async () => {
    const user = userEvent.setup();
    serveAssigned([]);
    mocks.post.mockResolvedValue({
      data: null,
      error: { type: 'AI.AGENT.NOT_FOUND', detail: 'agent not found', status: 404 },
    });
    renderSection();

    await pickAgent(user, 'Planner');

    expect(mocks.toastShow).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' }));
  });
});
