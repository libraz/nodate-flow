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
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

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
  let originalOffsetHeight: PropertyDescriptor | undefined;
  let originalOffsetWidth: PropertyDescriptor | undefined;
  let originalResizeObserver: PropertyDescriptor | undefined;

  beforeAll(() => {
    // Browser layout supplies a nonzero viewport through offset sizes and
    // ResizeObserver. happy-dom has no layout engine, so provide those
    // measurements for the scroll grid and its rows.
    originalOffsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight');
    originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');
    originalResizeObserver = Object.getOwnPropertyDescriptor(window, 'ResizeObserver');

    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      get() {
        return this.getAttribute('role') === 'grid' ? 600 : 36;
      },
    });
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
      configurable: true,
      value: 600,
    });

    class LayoutResizeObserver implements ResizeObserver {
      constructor(private readonly callback: ResizeObserverCallback) {}

      observe(target: Element): void {
        const blockSize = target.getAttribute('role') === 'grid' ? 600 : 36;
        const boxSize = { inlineSize: 600, blockSize };
        const entry = {
          target,
          contentRect: {
            x: 0,
            y: 0,
            width: 600,
            height: blockSize,
            top: 0,
            right: 600,
            bottom: blockSize,
            left: 0,
            toJSON: () => ({}),
          },
          borderBoxSize: [boxSize],
          contentBoxSize: [boxSize],
          devicePixelContentBoxSize: [boxSize],
        } as unknown as ResizeObserverEntry;

        queueMicrotask(() => this.callback([entry], this));
      }

      unobserve(): void {}

      disconnect(): void {}
    }

    Object.defineProperty(window, 'ResizeObserver', {
      configurable: true,
      value: LayoutResizeObserver,
    });
  });

  afterAll(() => {
    if (originalOffsetHeight) {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', originalOffsetHeight);
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, 'offsetHeight');
    }
    if (originalOffsetWidth) {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth);
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, 'offsetWidth');
    }
    if (originalResizeObserver) {
      Object.defineProperty(window, 'ResizeObserver', originalResizeObserver);
    } else {
      Reflect.deleteProperty(window, 'ResizeObserver');
    }
  });

  afterEach(() => {
    state.tasks = [];
    state.updateCalls = [];
    vi.clearAllMocks();
  });

  it('commits to the task captured at edit start even after the row order changes underneath it', async () => {
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
    const rowATitleText = await screen.findByText('Original A');
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

    const input = await screen.findByRole('textbox', { name: 'tasks.inline.edit_title' });
    fireEvent.change(input, { target: { value: 'Renamed while editing' } });
    fireEvent.blur(input);

    expect(state.updateCalls).toHaveLength(1);
    expect(state.updateCalls[0]?.id).toBe('task-A');
    expect(state.updateCalls[0]?.patch).toMatchObject({ title: 'Renamed while editing' });
  });
});
