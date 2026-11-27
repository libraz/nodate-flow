/**
 * Subtask nesting in the project list: unfiltered it asks for top-level rows
 * and lets a row's chevron reveal its children beneath it; with a filter or
 * search active it lists every match flat and each subtask names its parent.
 */

import { QueryClient, QueryClientProvider, useSuspenseQuery } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import type { TaskListItem } from '../api';

const state = vi.hoisted(() => ({
  filters: {} as Record<string, unknown>,
  listOptions: [] as unknown[],
  subtaskFetches: [] as string[],
}));

function makeTask(id: string, title: string, extra: Partial<TaskListItem> = {}): TaskListItem {
  return {
    id,
    title,
    derivedState: 'open',
    priority: 0,
    assigneeCount: 0,
    assignees: [],
    subtaskCount: 0,
    commentCount: 0,
    updatedAt: 0,
    ...extra,
  } as unknown as TaskListItem;
}

const parent = makeTask('P', 'Parent task', { subtaskCount: 1 });
const child = makeTask('C', 'Child task', {
  parentTaskId: 'P',
  parentTaskTitle: 'Parent task',
  subtaskCount: 1,
});
const grandchild = makeTask('G', 'Grandchild task', { parentTaskId: 'C' });
const children: Record<string, TaskListItem[]> = { P: [child], C: [grandchild] };

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useTasksInfiniteQuery: (_p: string, filters: Record<string, unknown>, options: unknown) => {
      state.listOptions.push(options);
      const flat = filters.search ? [child] : [parent];
      return {
        data: { pages: [{ tasks: flat }] },
        hasNextPage: false,
        isFetchingNextPage: false,
        fetchNextPage: vi.fn(),
      };
    },
    // The real hook's cache contract, without the network.
    useSubtasksQuery: (parentId: string) =>
      useSuspenseQuery({
        queryKey: actual.tasksKeys.children(parentId),
        queryFn: async () => {
          state.subtaskFetches.push(parentId);
          const tasks = children[parentId] ?? [];
          return { tasks, total: tasks.length };
        },
      }),
    useUpdateTask: () => ({ mutateAsync: vi.fn(() => Promise.resolve()), isPending: false }),
    useDeleteTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useReorderTasks: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

vi.mock('../use-task-filters', () => ({
  useTaskFilters: () => state.filters,
  resetTaskFilters: vi.fn(),
}));

vi.mock('../../projects/api', async () => {
  const actual = await vi.importActual<typeof import('../../projects/api')>('../../projects/api');
  const edges: never[] = [];
  return {
    ...actual,
    useProjectDependenciesQuery: () => ({ data: edges }),
    useProjectQuery: () => ({ data: { id: 'proj-001', workspaceId: 'ws-001' } }),
  };
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
  resources: {
    en: {
      common: {
        tasks: {
          hierarchy: {
            show_subtasks: 'Show subtasks of "{{title}}"',
            hide_subtasks: 'Hide subtasks of "{{title}}"',
          },
        },
      },
    },
  },
  interpolation: { escapeValue: false },
  parseMissingKeyHandler: (key: string) => key,
  react: { useSuspense: false },
});

function renderList(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <I18nextProvider i18n={i18nInstance}>
      <QueryClientProvider client={client}>
        <TaskListView projectId="proj-001" />
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('TaskListView subtask nesting', () => {
  beforeAll(() => {
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 600 });
  });

  beforeEach(() => {
    state.filters = {};
    state.listOptions = [];
    state.subtaskFetches = [];
  });

  it('requests top-level rows and fetches children only once a row is expanded', async () => {
    renderList();
    expect(state.listOptions.at(-1)).toEqual({ topLevel: true });
    expect(screen.queryByText('Child task')).toBeNull();
    expect(state.subtaskFetches).toEqual([]);

    const toggle = screen.getByRole('button', { name: 'Show subtasks of "Parent task"' });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(toggle);

    expect(await screen.findByText('Child task')).toBeTruthy();
    expect(state.subtaskFetches).toEqual(['P']);
    const hide = screen.getByRole('button', { name: 'Hide subtasks of "Parent task"' });
    expect(hide.getAttribute('aria-expanded')).toBe('true');

    // Nested beneath its parent, the child carries no parent line of its own.
    expect(screen.getAllByText('Parent task')).toHaveLength(1);
  });

  it('nests a child that has children of its own', async () => {
    renderList();
    fireEvent.click(screen.getByRole('button', { name: 'Show subtasks of "Parent task"' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Show subtasks of "Child task"' }));
    expect(await screen.findByText('Grandchild task')).toBeTruthy();
    expect(state.subtaskFetches).toEqual(['P', 'C']);
  });

  it('collapses again and hides the children', async () => {
    renderList();
    fireEvent.click(screen.getByRole('button', { name: 'Show subtasks of "Parent task"' }));
    await screen.findByText('Child task');
    fireEvent.click(screen.getByRole('button', { name: 'Hide subtasks of "Parent task"' }));
    await waitFor(() => expect(screen.queryByText('Child task')).toBeNull());
  });

  it('lists matches flat with the parent line and no chevrons while a search is active', () => {
    state.filters = { search: 'child' };
    renderList();
    expect(state.listOptions.at(-1)).toEqual({ topLevel: false });
    expect(screen.queryByRole('button', { name: /subtasks of/ })).toBeNull();
    // ParentTaskLine renders the parent's title as a link beside the child's own.
    expect(screen.getByText('Child task')).toBeTruthy();
    expect(screen.getByText('Parent task')).toBeTruthy();
  });
});
