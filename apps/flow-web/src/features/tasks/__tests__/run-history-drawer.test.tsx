/**
 * Run history: runs keyed by runKey, steps loaded on first open, and
 * pre-runKey runs paired from their start and end events.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: mocks.get },
  authSdk: { GET: mocks.get },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, hash }: { children: ReactNode; hash?: string }) => (
    <a href={`#${hash ?? ''}`}>{children}</a>
  ),
}));

import AgentRunHistoryDrawer, { groupRuns } from '../agent-panel/run-history-drawer';
import type { AgentRunEvent } from '../api';

const AGENT = { id: 'agent-1', name: 'Planner' };
const KEY_A = '11111111-1111-4111-8111-111111111111';
const KEY_B = '22222222-2222-4222-8222-222222222222';

function event(
  eventId: string,
  type: string,
  occurredAt: number,
  payload: Record<string, unknown>,
): AgentRunEvent {
  return {
    eventId,
    type,
    occurredAt,
    agent: AGENT,
    wasReversed: false,
    payloadJson: JSON.stringify(payload),
  };
}

/** Newest first, as the endpoint returns them. */
const RUNS: AgentRunEvent[] = [
  event('e6', 'ai.agent.run.completed', 600, {
    runKey: KEY_B,
    mode: 'task_work',
    endedReason: 'done',
  }),
  event('e5', 'ai.agent.run.started', 590, { runKey: KEY_B, mode: 'task_work' }),
  event('e4', 'ai.agent.run.failed', 400, { runKey: KEY_A, endedReason: 'budget', error: 'cap' }),
  event('e3', 'ai.agent.run.started', 390, { runKey: KEY_A }),
  event('e2', 'ai.agent.run.completed', 200, { confidence: 0.9 }),
  event('e1', 'ai.agent.run.started', 190, {}),
];

function ok(data: unknown): unknown {
  return { data, error: null, response: new Response(null, { status: 200 }) };
}

const STEPS = [
  {
    step: 1,
    tool: 'set_priority',
    thought: 'Looks urgent',
    argsSummary: 'priority=3',
    outcome: 'proposed',
    proposalId: 'p-9',
    createdAt: 595,
  },
  {
    step: 2,
    tool: 'finish',
    thought: '',
    argsSummary: '',
    outcome: 'applied',
    createdAt: 596,
  },
];

function serve(): void {
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/tasks/{id}/agent-runs') return ok({ runs: RUNS, total: RUNS.length });
    if (path === '/tasks/{id}/agent-runs/{runKey}/steps') {
      return ok({ steps: STEPS, total: STEPS.length });
    }
    return {
      data: null,
      error: { type: 'INTERNAL.UNEXPECTED', detail: `unmocked ${path}` },
      response: new Response(null, { status: 400 }),
    };
  });
}

function renderDrawer(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <AgentRunHistoryDrawer taskId="task-1" locale="en" open onClose={() => undefined} />
    </QueryClientProvider>,
  );
}

function stepCalls(): number {
  return mocks.get.mock.calls.filter(([path]) => path === '/tasks/{id}/agent-runs/{runKey}/steps')
    .length;
}

beforeEach(() => {
  mocks.get.mockReset();
  serve();
});

describe('groupRuns', () => {
  it('joins keyed events by runKey and pairs old events by adjacency, newest first', () => {
    const groups = groupRuns(RUNS);
    expect(groups.map((g) => g.id)).toEqual([KEY_B, KEY_A, 'e1']);
    expect(groups[0]?.started?.eventId).toBe('e5');
    expect(groups[0]?.ended?.eventId).toBe('e6');
    expect(groups[2]?.started?.eventId).toBe('e1');
    expect(groups[2]?.ended?.eventId).toBe('e2');
    expect(groups[2]?.runKey).toBeUndefined();
  });

  it('keeps a start with no end as one open run', () => {
    const groups = groupRuns([event('s', 'ai.agent.run.started', 5, { runKey: KEY_A })]);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.ended).toBeUndefined();
  });
});

describe('AgentRunHistoryDrawer', () => {
  it('lists one row per run with its end reason as the summary', async () => {
    renderDrawer();

    expect(await screen.findByText('task_detail.agent.history.ended.done')).toBeDefined();
    expect(screen.getByText('task_detail.agent.history.ended.budget')).toBeDefined();
    expect(screen.getAllByRole('listitem').length).toBeGreaterThanOrEqual(3);
    // Old runs have no step log, so they are not disclosures.
    expect(screen.getAllByRole('button', { expanded: false })).toHaveLength(2);
  });

  it('loads steps only when a run is opened', async () => {
    const user = userEvent.setup();
    renderDrawer();

    const [newest] = await screen.findAllByRole('button', { expanded: false });
    expect(stepCalls()).toBe(0);

    await user.click(newest as HTMLElement);

    await waitFor(() => {
      expect(stepCalls()).toBe(1);
    });
    const [, init] = mocks.get.mock.calls.find(
      ([path]) => path === '/tasks/{id}/agent-runs/{runKey}/steps',
    ) as [string, { params: unknown }];
    expect(init.params).toEqual({ path: { id: 'task-1', runKey: KEY_B } });
    expect(newest?.getAttribute('aria-expanded')).toBe('true');

    expect(await screen.findByText('task_detail.agent.proposals.ops.set_priority')).toBeDefined();
    expect(screen.getByText('task_detail.agent.history.outcome.proposed')).toBeDefined();
    expect(screen.getByText('Looks urgent')).toBeDefined();
    const link = screen.getByRole('link', { name: 'task_detail.agent.history.view_proposal' });
    expect(link.getAttribute('href')).toBe('#agent-proposal-p-9');
    const ordered = screen.getByText('Looks urgent').closest('ol');
    expect(within(ordered as HTMLElement).getAllByRole('listitem')).toHaveLength(2);
  });
});
