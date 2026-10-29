/**
 * A parent re-render — a refetch landing, a selection change, a drag
 * hover — must not remount the list view's cells. TanStack renders each
 * column `cell` function as a component type, so a column model rebuilt
 * per render replaced every cell, and an open inline editor lost its
 * input (and the user's focus) mid-edit.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import type { TaskListItem } from '../api';

function makeTask(id: string, title: string): TaskListItem {
  return {
    id,
    title,
    derivedState: 'open',
    priority: 0,
    assigneeCount: 0,
    updatedAt: 0,
  } as unknown as TaskListItem;
}

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    // Fresh objects on every call, as a refetch would deliver.
    useTasksInfiniteQuery: () => ({
      data: { pages: [{ tasks: [makeTask('task-A', 'Task A'), makeTask('task-B', 'Task B')] }] },
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    }),
    useUpdateTask: () => ({ mutateAsync: vi.fn(() => Promise.resolve()), isPending: false }),
    useDeleteTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useReorderTasks: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

vi.mock('../../projects/api', async () => {
  const actual = await vi.importActual<typeof import('../../projects/api')>('../../projects/api');
  // A query hands back the same reference until its data changes.
  const edges: never[] = [];
  return { ...actual, useProjectDependenciesQuery: () => ({ data: edges }) };
});

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    to: _to,
    params: _params,
    ...rest
  }: {
    children: ReactNode;
    to: string;
    params: unknown;
  } & Record<string, unknown>) => <a {...rest}>{children}</a>,
  useNavigate: () => vi.fn(),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: vi.fn() } }));

import TaskListView from '../task-list-view';

const i18nInstance = i18n.createInstance();
void i18nInstance.use(initReactI18next).init({
  lng: 'en',
  defaultNS: 'common',
  ns: ['common'],
  resources: { en: { common: {} } },
  parseMissingKeyHandler: (key: string) => key,
  react: { useSuspense: false },
});
const client = new QueryClient();

function Harness({ tick }: { tick: number }): ReactElement {
  return (
    <I18nextProvider i18n={i18nInstance}>
      <QueryClientProvider client={client}>
        <div data-tick={tick}>
          <TaskListView projectId="proj-001" />
        </div>
      </QueryClientProvider>
    </I18nextProvider>
  );
}

describe('TaskListView cell stability', () => {
  beforeAll(() => {
    // The virtualizer renders nothing in a zero-sized viewport.
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 600 });
  });

  it('keeps the same focused inline-edit input across a parent re-render', () => {
    const { rerender } = render(<Harness tick={0} />);

    fireEvent.doubleClick(screen.getByText('Task A'));
    const input = screen.getByRole('textbox', { name: 'tasks.inline.edit_title' });
    input.focus();
    fireEvent.change(input, { target: { value: 'Task A edited' } });

    rerender(<Harness tick={1} />);

    const after = screen.getByRole('textbox', { name: 'tasks.inline.edit_title' });
    expect(after).toBe(input);
    expect(document.activeElement).toBe(input);
    expect((after as HTMLInputElement).value).toBe('Task A edited');
  });
});
