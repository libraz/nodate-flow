/**
 * An inline spreadsheet cell edit must commit to the task
 * whose id was captured when editing started, not to whatever task
 * currently sits at that row index. Tasks re-sort or refetch (filter
 * change, another user's edit landing) while a cell is being edited,
 * and a commit keyed by rowIdx alone would silently patch a different
 * task than the one the user was looking at.
 *
 * This starts an edit on row 0 (task A), swaps the underlying task
 * list so a different task (B) now occupies row 0, then commits — and
 * asserts the PATCH went to task A's id, never task B's.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

import type { TaskListItem } from '../api';

const state = vi.hoisted(() => ({
  tasks: [] as TaskListItem[],
  updateCalls: [] as { id: string; patch: Record<string, unknown> }[],
}));

function makeTask(id: string, title: string): TaskListItem {
  return {
    id,
    title,
    derivedState: 'open',
    priority: 2,
    dueOn: null,
  } as unknown as TaskListItem;
}

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useTasksQuery: () => ({ data: state.tasks }),
    useUpdateTask: () => ({
      mutateAsync: vi.fn((args: { id: string; patch: Record<string, unknown> }) => {
        state.updateCalls.push(args);
        return Promise.resolve({});
      }),
      isPending: false,
    }),
    useDeleteTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

import TaskSpreadsheetView from '../task-spreadsheet-view';

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'common',
    ns: ['common'],
    resources: { en: { common: {} } },
    interpolation: { escapeValue: false },
    parseMissingKeyHandler: (key: string) => key,
    react: { useSuspense: false },
  });
  return instance;
}

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <I18nextProvider i18n={testI18n()}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </I18nextProvider>
  );
}

describe('task-spreadsheet-view inline edit identity', () => {
  beforeAll(() => {
    // @tanstack/react-virtual sizes its viewport from offsetHeight/Width,
    // which jsdom/happy-dom report as 0. Without a nonzero viewport the
    // virtualizer computes an empty visible range and no rows render at
    // all, which would make this test pass vacuously.
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      value: 600,
    });
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
      configurable: true,
      value: 600,
    });
  });

  afterEach(() => {
    state.tasks = [];
    state.updateCalls = [];
    vi.clearAllMocks();
  });

  it('commits to the task captured at edit start even after the row order changes underneath it', () => {
    state.tasks = [makeTask('task-A', 'Original A'), makeTask('task-B', 'Original B')];

    const { rerender } = render(
      <Wrapper>
        <TaskSpreadsheetView projectId="proj-001" />
      </Wrapper>,
    );

    // Start editing row 0's title — this is task A. Query the title
    // text itself (not role=gridcell — the checkbox cell in the same
    // row also computes an accessible name from its aria-labelled
    // checkbox, "Original A", which collides with a role-based query).
    const rowATitleText = screen.getByText('Original A');
    fireEvent.click(rowATitleText);

    // Row order flips underneath the open edit: task B now occupies row 0.
    // A real cause would be a filter/sort change or another actor's write
    // landing via query invalidation mid-edit. React reconciles by task id,
    // so the DOM node that WAS row 0 (task A, now editing) keeps its
    // identity as task A but moves to row index 1, and the edit input
    // itself re-mounts onto whichever DOM node is now at row index 0 —
    // task B's row. Re-querying after the swap follows the input to
    // wherever React actually put it, exactly like a real user would.
    state.tasks = [makeTask('task-B', 'Original B'), makeTask('task-A', 'Original A')];
    rerender(
      <Wrapper>
        <TaskSpreadsheetView projectId="proj-001" />
      </Wrapper>,
    );

    const input = screen.getByRole('textbox', { name: 'tasks.inline.edit_title' });
    fireEvent.change(input, { target: { value: 'Renamed while editing' } });
    fireEvent.blur(input);

    expect(state.updateCalls).toHaveLength(1);
    expect(state.updateCalls[0]?.id).toBe('task-A');
    expect(state.updateCalls[0]?.patch).toMatchObject({ title: 'Renamed while editing' });
  });
});
