/**
 * "Run now" on an assigned agent: enabled only when the agent could
 * actually run, with the reason shown as visible text when it cannot.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
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
  useTranslation: () => ({ t: (key: string) => key }),
}));

import AgentRunNow, { blockedReason } from '../agent-panel/agent-run-now';
import type { TaskAgentActor } from '../api';

const TASK = {
  id: 'task-1',
  title: 'Ship it',
  derivedState: 'open',
  visibility: 'public',
  priority: 0,
  projectId: 'p-1',
  workspaceId: 'ws-1',
};

const ACTOR = {
  id: 'actor-1',
  agentId: 'agent-1',
  agentName: 'Planner',
  role: 'assignee',
  kind: 'task_agent',
  paused: false,
  hasGrants: true,
  createdAt: 0,
} as TaskAgentActor;

function ok(data: unknown): unknown {
  return { data, error: null, response: new Response(null, { status: 200 }) };
}

function renderRunNow(actor: TaskAgentActor, task: Record<string, unknown>, canAct = true): void {
  mocks.get.mockResolvedValue(ok({ ...TASK, ...task }));
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
      <AgentRunNow taskId="task-1" actor={actor} canAct={canAct} />
    </Wrapper>,
  );
}

const LABEL = 'task_detail.agent.run_now.label';

beforeEach(() => {
  mocks.get.mockReset();
  mocks.post.mockReset().mockResolvedValue(ok({ runRequestedAt: 1_700_000_000 }));
  mocks.toastShow.mockReset();
});

describe('blockedReason', () => {
  const task = { visibility: 'public', derivedState: 'open' } as const;

  it('is null when the agent can run', () => {
    expect(blockedReason({ paused: false, hasGrants: true }, task, true)).toBeNull();
  });

  it('names the first thing a person can fix', () => {
    expect(blockedReason({ paused: true, hasGrants: false }, task, false)).toBe('no_permission');
    expect(blockedReason({ paused: true, hasGrants: false }, task, true)).toBe('paused');
    expect(blockedReason({ paused: false, hasGrants: false }, task, true)).toBe('no_grants');
    expect(
      blockedReason({ paused: false, hasGrants: true }, { ...task, visibility: 'private' }, true),
    ).toBe('private');
    expect(
      blockedReason({ paused: false, hasGrants: true }, { ...task, visibility: 'project' }, true),
    ).toBe('private');
  });

  it('treats finished and archived tasks as not workable', () => {
    for (const derivedState of ['done', 'cancelled'] as const) {
      expect(
        blockedReason({ paused: false, hasGrants: true }, { ...task, derivedState }, true),
      ).toBe('not_workable');
    }
    expect(
      blockedReason({ paused: false, hasGrants: true }, { ...task, archivedAt: 5 }, true),
    ).toBe('not_workable');
  });
});

describe('AgentRunNow', () => {
  it('requests a run through the agent run endpoint', async () => {
    const user = userEvent.setup();
    renderRunNow(ACTOR, {});

    await user.click(await screen.findByRole('button', { name: LABEL }));

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledTimes(1);
    });
    const [path, init] = mocks.post.mock.calls[0] as [string, { params: unknown }];
    expect(path).toBe('/tasks/{id}/agents/{agentId}/run');
    expect(init.params).toEqual({ path: { id: 'task-1', agentId: 'agent-1' } });
    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalledWith(expect.objectContaining({ tone: 'success' }));
    });
  });

  it.each([
    ['paused', { ...ACTOR, paused: true }, {}, 'paused'],
    ['no grants', { ...ACTOR, hasGrants: false }, {}, 'no_grants'],
    ['finished task', ACTOR, { derivedState: 'done' }, 'not_workable'],
    ['private task', ACTOR, { visibility: 'private' }, 'private'],
  ] as const)(
    'disables the button with the %s reason as visible text',
    async (_n, actor, task, reason) => {
      renderRunNow(actor as TaskAgentActor, task);

      const button = await screen.findByRole('button', { name: LABEL });
      expect((button as HTMLButtonElement).disabled).toBe(true);
      const text = screen.getByText(`task_detail.agent.run_now.reasons.${reason}`);
      expect(button.getAttribute('aria-describedby')).toBe(text.id);
    },
  );

  it('explains a viewer who may not run agents', async () => {
    renderRunNow(ACTOR, {}, false);

    const button = await screen.findByRole('button', { name: LABEL });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('task_detail.agent.run_now.reasons.no_permission')).toBeDefined();
  });

  it('reports a refused run as a toast', async () => {
    const user = userEvent.setup();
    mocks.post.mockResolvedValue({
      data: null,
      error: { type: 'AI.COST.GUARD_EXCEEDED', detail: 'budget', status: 402 },
      response: new Response(null, { status: 402 }),
    });
    renderRunNow(ACTOR, {});

    await user.click(await screen.findByRole('button', { name: LABEL }));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' }));
    });
  });
});
