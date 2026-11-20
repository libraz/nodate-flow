/**
 * Each widget body has to say something in every state: loading, empty
 * (what produces items), error, and a capped list with a "see all" link.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ reminders: vi.fn(), digest: vi.fn() }));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      [key, ...Object.values(params ?? {})].join(' '),
    i18n: { resolvedLanguage: 'en' },
  }),
}));

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }): ReactElement => <a href="/x">{children}</a>,
}));

vi.mock('../../reminders/api', () => ({ useRemindersQuery: mocks.reminders }));
vi.mock('../../weekly-digest/api', () => ({ useWeeklyDigestQuery: mocks.digest }));

import BurndownWidget from '../widgets/burndown-widget';
import OverdueTasksWidget from '../widgets/overdue-tasks-widget';
import TaskSummaryWidget from '../widgets/task-summary-widget';

function reminder(i: number, kind = 'overdue'): Record<string, unknown> {
  return {
    taskId: `t${i}`,
    title: `Task ${i}`,
    kind,
    dueOn: '2026-11-01',
    daysUntilDue: -i,
    message: '',
    state: 'open',
  };
}

function wrap(ui: ReactElement): ReactElement {
  return <QueryClientProvider client={new QueryClient()}>{ui}</QueryClientProvider>;
}

describe('dashboard widgets', () => {
  beforeEach(() => {
    mocks.reminders.mockReset();
    mocks.digest.mockReset();
  });

  it('overdue: shows a skeleton while loading', () => {
    mocks.reminders.mockReturnValue({ isPending: true, isError: false });
    render(wrap(<OverdueTasksWidget workspaceId="ws" />));
    expect(screen.getByRole('status')).toBeTruthy();
  });

  it('overdue: shows an error with retry', () => {
    mocks.reminders.mockReturnValue({ isPending: false, isError: true, refetch: vi.fn() });
    render(wrap(<OverdueTasksWidget workspaceId="ws" />));
    expect(screen.getByRole('alert').textContent).toContain('widgets.error_title');
  });

  it('overdue: explains what produces items when empty', () => {
    mocks.reminders.mockReturnValue({
      isPending: false,
      isError: false,
      data: [reminder(1, 'due_today')],
    });
    render(wrap(<OverdueTasksWidget workspaceId="ws" />));
    expect(screen.getByText('widgets.overdue_tasks.empty_hint')).toBeTruthy();
  });

  it('overdue: keeps only overdue reminders, capped at five, with a see-all link', () => {
    mocks.reminders.mockReturnValue({
      isPending: false,
      isError: false,
      data: [1, 2, 3, 4, 5, 6, 7].map((i) => reminder(i)).concat(reminder(9, 'due_today')),
    });
    render(wrap(<OverdueTasksWidget workspaceId="ws" />));
    expect(screen.getAllByRole('listitem')).toHaveLength(5);
    expect(screen.queryByText('Task 9')).toBeNull();
    expect(screen.getByText('widgets.see_all')).toBeTruthy();
  });

  it('task summary: renders counts as labelled figures', () => {
    mocks.digest.mockReturnValue({
      data: {
        counts: { open: 4, review: 1, waiting: 2, done: 7, cancelled: 0 },
        overdueOpen: [{}, {}],
      },
    });
    render(wrap(<TaskSummaryWidget workspaceId="ws" />));
    expect(screen.getByText('widgets.task_summary.overdue')).toBeTruthy();
    expect(screen.getByText('7')).toBeTruthy();
  });

  it('task summary: says what fills it when the workspace has no tasks', () => {
    mocks.digest.mockReturnValue({
      data: {
        counts: { open: 0, review: 0, waiting: 0, done: 0, cancelled: 0 },
        overdueOpen: null,
      },
    });
    render(wrap(<TaskSummaryWidget workspaceId="ws" />));
    expect(screen.getByText('widgets.task_summary.empty_hint')).toBeTruthy();
  });

  it('burndown: renders a coming-soon body', () => {
    render(wrap(<BurndownWidget />));
    expect(screen.getByText('widgets.burndown.coming_soon_title')).toBeTruthy();
  });
});
