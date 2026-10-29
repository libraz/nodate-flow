/**
 * Reordering in the list view: a drag starts on the handle, but the drop
 * target is the whole row, so releasing over any cell of another row
 * moves the task there and the row shows the drop indicator meanwhile.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import type { TaskListItem } from '../api';

const state = vi.hoisted(() => ({
  reorder: vi.fn((_args: { projectId: string; items: { id: string; sortWeight: number }[] }) =>
    Promise.resolve(),
  ),
}));

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
    useTasksInfiniteQuery: () => ({
      data: { pages: [{ tasks: [makeTask('task-A', 'Task A'), makeTask('task-B', 'Task B')] }] },
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    }),
    useUpdateTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useDeleteTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useReorderTasks: () => ({ mutateAsync: state.reorder, isPending: false }),
  };
});

vi.mock('../../projects/api', async () => {
  const actual = await vi.importActual<typeof import('../../projects/api')>('../../projects/api');
  return { ...actual, useProjectDependenciesQuery: () => ({ data: [] }) };
});

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <span>{children}</span>,
  useNavigate: () => vi.fn(),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: vi.fn() } }));

import TaskListView from '../task-list-view';

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    defaultNS: 'common',
    ns: ['common'],
    resources: { en: { common: {} } },
    parseMissingKeyHandler: (key: string) => key,
    react: { useSuspense: false },
  });
  return instance;
}

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  return (
    <I18nextProvider i18n={testI18n()}>
      <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>
    </I18nextProvider>
  );
}

describe('TaskListView row drop target', () => {
  beforeAll(() => {
    // The virtualizer renders nothing in a zero-sized viewport.
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 600 });
  });

  it('accepts a drop on any cell of the target row', () => {
    render(
      <Wrapper>
        <TaskListView projectId="proj-001" />
      </Wrapper>,
    );

    // Cells re-render (and are re-created) as the drop target moves, so
    // each event goes to the cell currently on screen, as a pointer would.
    const rowOf = (): Element | null => screen.getByText('Task B').closest('[role="row"]');

    fireEvent.dragOver(screen.getByText('Task B'), { dataTransfer: new DataTransfer() });
    expect(rowOf()?.getAttribute('data-drop-target')).toBe('true');

    // The handle's dragstart stored the source row index; row 0 is task A.
    const dataTransfer = new DataTransfer();
    dataTransfer.setData('text/plain', '0');
    fireEvent.drop(screen.getByText('Task B'), { dataTransfer });
    expect(rowOf()?.getAttribute('data-drop-target')).toBeNull();
    expect(state.reorder).toHaveBeenCalledTimes(1);
    expect(state.reorder.mock.calls[0]?.[0].items.map((item) => item.id)).toEqual([
      'task-B',
      'task-A',
    ]);
  });
});
