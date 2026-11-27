/**
 * Subtask nesting in the spreadsheet: the chevron expands a row's children in
 * display order without starting a cell edit, and an active filter lists
 * matches flat with the parent line.
 */

import { QueryClient, QueryClientProvider, useSuspenseQuery } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { TaskListItem } from '../api';

const state = vi.hoisted(() => ({
  filters: {} as Record<string, unknown>,
  listOptions: [] as unknown[],
}));

function makeTask(id: string, title: string, extra: Partial<TaskListItem> = {}): TaskListItem {
  return {
    id,
    title,
    derivedState: 'open',
    priority: 0,
    dueOn: null,
    subtaskCount: 0,
    updatedAt: 0,
    ...extra,
  } as unknown as TaskListItem;
}

const first = makeTask('P', 'Parent task', { subtaskCount: 1 });
const second = makeTask('Q', 'Second task');
const child = makeTask('C', 'Child task', { parentTaskId: 'P', parentTaskTitle: 'Parent task' });

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useTasksQuery: (_p: string, filters: Record<string, unknown>, options: unknown) => {
      state.listOptions.push(options);
      return { data: filters.search ? [child] : [first, second] };
    },
    useSubtasksQuery: (parentId: string) =>
      useSuspenseQuery({
        queryKey: actual.tasksKeys.children(parentId),
        queryFn: async () => ({ tasks: [child], total: 1 }),
      }),
    useUpdateTask: () => ({ mutateAsync: vi.fn(() => Promise.resolve({})), isPending: false }),
    useDeleteTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

vi.mock('../use-task-filters', () => ({ useTaskFilters: () => state.filters }));

vi.mock('../../projects/api', async () => {
  const actual = await vi.importActual<typeof import('../../projects/api')>('../../projects/api');
  return {
    ...actual,
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
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: vi.fn() } }));

vi.mock('@tanstack/react-virtual', async () => {
  const virtual =
    await vi.importActual<typeof import('@tanstack/react-virtual')>('@tanstack/react-virtual');
  return {
    ...virtual,
    useVirtualizer: (options: Parameters<typeof virtual.useVirtualizer>[0]) =>
      virtual.useVirtualizer({
        ...options,
        initialRect: { width: 600, height: 600 },
        observeElementRect: (_virtualizer, observe) => {
          observe({ width: 600, height: 600 });
          return () => {};
        },
      }),
  };
});

import TaskSpreadsheetView from '../task-spreadsheet-view';

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

function renderSheet(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <I18nextProvider i18n={i18nInstance}>
      <QueryClientProvider client={client}>
        <TaskSpreadsheetView projectId="proj-001" />
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

const titleOrder = (): string[] =>
  screen
    .getAllByRole('row')
    .slice(1)
    .map((row) => row.textContent ?? '');

describe('TaskSpreadsheetView subtask nesting', () => {
  beforeEach(() => {
    state.filters = {};
    state.listOptions = [];
  });

  it('expands children directly beneath the parent without starting an edit', async () => {
    renderSheet();
    expect(state.listOptions.at(-1)).toEqual({ topLevel: true });

    const toggle = screen.getByRole('button', { name: 'Show subtasks of "Parent task"' });
    fireEvent.click(toggle);

    await screen.findByText('Child task');
    expect(screen.queryByRole('textbox')).toBeNull();
    const order = titleOrder();
    expect(order[0]).toContain('Parent task');
    expect(order[1]).toContain('Child task');
    expect(order[2]).toContain('Second task');
    expect(
      screen
        .getByRole('button', { name: 'Hide subtasks of "Parent task"' })
        .getAttribute('aria-expanded'),
    ).toBe('true');
  });

  it('lists matches flat with the parent line and no chevrons while filtered', () => {
    state.filters = { search: 'child' };
    renderSheet();
    expect(state.listOptions.at(-1)).toEqual({ topLevel: false });
    expect(screen.queryByRole('button', { name: /subtasks of/ })).toBeNull();
    expect(screen.getByText('Child task')).toBeTruthy();
    expect(screen.getByText('Parent task')).toBeTruthy();
  });
});
