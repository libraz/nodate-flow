/**
 * Pending agent proposals on a task: what is shown, how a decision is
 * sent, and what a refusal does (toast and a refetch of the list).
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { Suspense } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: mocks.get, POST: mocks.post },
  authSdk: { GET: mocks.get, POST: mocks.post },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, vars?: Record<string, unknown>) =>
      vars?.time ? `${key}:${String(vars.time)}` : key,
  }),
}));

import { keysForEvent } from '../../realtime/event-to-keys';
import AgentProposals from '../agent-panel/agent-proposals';
import { agentWorkKeys } from '../agent-panel/agent-work-api';

const FUTURE = Math.floor(Date.now() / 1000) + 3 * 86_400;

function proposal(over: Record<string, unknown>): Record<string, unknown> {
  return {
    id: 'p-1',
    agent: { id: 'agent-1', name: 'Planner' },
    kind: 'set_priority',
    args: { priority: 3 },
    before: { priority: 1 },
    status: 'pending',
    createdAt: FUTURE - 7 * 86_400,
    expiresAt: FUTURE,
    ...over,
  };
}

const LONG_BODY = `${'Please double check the rollout plan. '.repeat(5)}Thanks.`;

function ok(data: unknown): unknown {
  return { data, error: null, response: new Response(null, { status: 200 }) };
}

function serve(proposals: Record<string, unknown>[]): void {
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/tasks/{id}/agent-proposals') return ok({ proposals, total: proposals.length });
    return {
      data: null,
      error: { type: 'INTERNAL.UNEXPECTED', detail: `unmocked ${path}` },
      response: new Response(null, { status: 400 }),
    };
  });
}

function renderList(canDecide = true): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Wrapper({ children }: { children: ReactNode }): ReactNode {
    return (
      <QueryClientProvider client={client}>
        <Suspense fallback={null}>{children}</Suspense>
      </QueryClientProvider>
    );
  }
  render(
    <Wrapper>
      <AgentProposals taskId="task-1" locale="en" canDecide={canDecide} />
    </Wrapper>,
  );
  return client;
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.post.mockReset().mockResolvedValue(ok({ id: 'p-1', status: 'applied' }));
  mocks.toastShow.mockReset();
});

describe('AgentProposals', () => {
  it('shows the operation, the expiry and the before-to-after change', async () => {
    serve([proposal({})]);
    renderList();

    expect(await screen.findByText('task_detail.agent.proposals.ops.set_priority')).toBeDefined();
    expect(screen.getByText('tasks.priority.low')).toBeDefined();
    expect(screen.getByText('tasks.priority.high')).toBeDefined();
    expect(
      screen.getByRole('img', { name: 'task_detail.agent.proposals.changes_to' }),
    ).toBeDefined();
    const time = document.querySelector('time');
    expect(time?.getAttribute('datetime')).toBe(new Date(FUTURE * 1000).toISOString());
    expect(time?.getAttribute('title')).toBeTruthy();
  });

  it('shows a muted dash for a missing value', async () => {
    serve([proposal({ kind: 'set_due_on', args: {}, before: { dueOn: '2026-12-01' } })]);
    renderList();

    expect(
      await screen.findByRole('img', { name: 'task_detail.agent.proposals.empty_value' }),
    ).toBeDefined();
  });

  it('expands a long comment body as plain text', async () => {
    const user = userEvent.setup();
    serve([proposal({ kind: 'add_comment', args: { body: LONG_BODY }, before: {} })]);
    renderList();

    const toggle = await screen.findByRole('button', {
      name: 'task_detail.agent.proposals.show_more',
    });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    await user.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(toggle.textContent).toBe('task_detail.agent.proposals.show_less');
    expect(screen.getByText(LONG_BODY).tagName).toBe('P');
  });

  it('approves through the proposal endpoint and refetches the list', async () => {
    const user = userEvent.setup();
    serve([proposal({})]);
    renderList();

    await user.click(
      await screen.findByRole('button', { name: 'task_detail.agent.proposals.approve' }),
    );

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledTimes(1);
    });
    const [path, init] = mocks.post.mock.calls[0] as [string, { params: unknown }];
    expect(path).toBe('/tasks/{id}/agent-proposals/{proposalId}/approve');
    expect(init.params).toEqual({ path: { id: 'task-1', proposalId: 'p-1' } });
    await waitFor(() => {
      expect(mocks.get.mock.calls.length).toBeGreaterThan(1);
    });
    expect(mocks.toastShow).toHaveBeenCalledWith(expect.objectContaining({ tone: 'success' }));
  });

  it('turns a stale refusal into a toast and still refetches', async () => {
    const user = userEvent.setup();
    serve([proposal({})]);
    mocks.post.mockResolvedValue({
      data: null,
      error: { type: 'WS.AGENT_PROPOSAL.STALE', detail: 'stale', status: 409 },
      response: new Response(null, { status: 409 }),
    });
    renderList();

    await user.click(
      await screen.findByRole('button', { name: 'task_detail.agent.proposals.approve' }),
    );

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' }));
    });
    await waitFor(() => {
      expect(mocks.get.mock.calls.length).toBeGreaterThan(1);
    });
  });

  it('rejects with the typed reason', async () => {
    const user = userEvent.setup();
    serve([proposal({})]);
    mocks.post.mockResolvedValue(ok({ id: 'p-1', status: 'rejected' }));
    renderList();

    await user.click(
      await screen.findByRole('button', { name: 'task_detail.agent.proposals.reject' }),
    );
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByRole('textbox'), 'Not this sprint');
    await user.click(
      within(dialog).getByRole('button', { name: 'task_detail.agent.proposals.reject_confirm' }),
    );

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledTimes(1);
    });
    const [path, init] = mocks.post.mock.calls[0] as [string, { body: unknown }];
    expect(path).toBe('/tasks/{id}/agent-proposals/{proposalId}/reject');
    expect(init.body).toEqual({ reason: 'Not this sprint' });
  });

  it('shows a note instead of buttons to someone who cannot act', async () => {
    serve([proposal({})]);
    renderList(false);

    expect(await screen.findByText('task_detail.agent.proposals.cannot_decide')).toBeDefined();
    expect(
      screen.queryByRole('button', { name: 'task_detail.agent.proposals.approve' }),
    ).toBeNull();
    expect(screen.queryByRole('button', { name: 'task_detail.agent.proposals.reject' })).toBeNull();
  });

  it('offers no decision on a proposal that has lapsed', async () => {
    serve([proposal({ status: 'expired' })]);
    renderList();

    expect(await screen.findByText('task_detail.agent.proposals.expired')).toBeDefined();
    expect(
      screen.queryByRole('button', { name: 'task_detail.agent.proposals.approve' }),
    ).toBeNull();
  });

  it('renders nothing when no proposal is pending', async () => {
    serve([]);
    renderList();

    await waitFor(() => {
      expect(mocks.get).toHaveBeenCalled();
    });
    expect(screen.queryByRole('region')).toBeNull();
  });
});

describe('proposal cache keys', () => {
  it('sit under the prefix the task.changed stream event invalidates', () => {
    const key = agentWorkKeys.proposals('task-1');
    const prefixes = keysForEvent({ kind: 'task.changed', workspaceId: 'ws-1', at: 0 });
    const covered = prefixes.some((prefix) => prefix.every((part, i) => key[i] === part));
    expect(covered).toBe(true);
  });
});
