/**
 * Component tests for TaskBoardView.
 *
 * Regression: empty kanban columns previously rendered a `<p>` directly
 * inside `<div role="list">`, which violated axe-core's
 * `aria-required-children` rule (a `role="list"` must contain at least
 * one `role="listitem"` descendant). The fix wraps the empty placeholder
 * in a single `role="listitem"` element so list semantics stay valid
 * even when a column is empty.
 */

import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import type { TaskListItem } from '../api';
import TaskBoardView, { transitionsByLanding } from '../task-board-view';

let mockTasks: TaskListItem[] = [];

vi.mock('../../labels/api', () => ({
  useLabelsQuery: () => ({
    data: [
      { id: 'lbl-1', name: 'Backend', color: '#3366cc' },
      { id: 'lbl-2', name: 'Urgent', color: '#cc3333' },
    ],
  }),
}));

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useTasksQuery: () => ({ data: mockTasks }),
    useTransitionTask: () => ({ mutate: vi.fn(), isPending: false }),
  };
});

vi.mock('../../projects/api', async () => {
  const actual = await vi.importActual<typeof import('../../projects/api')>('../../projects/api');
  return {
    ...actual,
    useProjectDependenciesQuery: () => ({ data: [] }),
    useProjectQuery: () => ({ data: { workspaceId: 'ws-001' } }),
  };
});

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

describe('<TaskBoardView>', () => {
  beforeEach(() => {
    mockTasks = [];
  });

  it('renders a listitem placeholder inside every empty column', () => {
    renderWithProviders(<TaskBoardView projectId="proj-001" />);

    // The four visible state columns are each their own list; Cancelled is a rail. With zero tasks,
    // each list must still expose at least one listitem so screen
    // readers (and axe) recognise the empty drop zone as a valid list.
    const lists = screen.getAllByRole('list');
    expect(lists).toHaveLength(4);
    for (const list of lists) {
      const items = within(list).getAllByRole('listitem');
      expect(items).toHaveLength(1);
      expect(items[0]?.textContent).toContain('tasks.board.empty_column');
    }
  });

  it('has no axe-core aria-required-children violations on empty board', async () => {
    const { container } = renderWithProviders(<TaskBoardView projectId="proj-001" />);

    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });

  it('shows a visible limit notice when the board reaches the query cap', () => {
    mockTasks = Array.from({ length: 200 }, (_, idx) => ({
      id: `task-${idx}`,
      title: `Task ${idx}`,
      derivedState: 'open',
      priority: 1,
      updatedAt: 0,
      projectId: 'proj-001',
      visibility: 'public',
    })) as TaskListItem[];

    renderWithProviders(<TaskBoardView projectId="proj-001" />);

    expect(screen.getByText(/tasks\.limit_notice/i)).toBeDefined();
  });

  it('shows resolved label chips on a card and skips unknown ids', () => {
    mockTasks = [
      {
        id: 'task-1',
        title: 'Tagged',
        derivedState: 'open',
        priority: 0,
        updatedAt: 0,
        projectId: 'proj-001',
        visibility: 'public',
        labelIds: ['lbl-1', 'gone', 'lbl-2'],
      },
    ] as TaskListItem[];

    renderWithProviders(<TaskBoardView projectId="proj-001" />);

    expect(screen.getByText('Backend')).toBeDefined();
    expect(screen.getByText('Urgent')).toBeDefined();
  });

  it('collapses Cancelled into a rail that expands and persists', async () => {
    window.localStorage.removeItem('nf:board-cancelled-open');
    renderWithProviders(<TaskBoardView projectId="proj-001" />);

    const rail = screen.getByRole('button', { name: 'tasks.board.rail.expand' });
    expect(rail.getAttribute('aria-expanded')).toBe('false');
    await userEvent.setup().click(rail);

    expect(screen.getAllByRole('list')).toHaveLength(5);
    expect(window.localStorage.getItem('nf:board-cancelled-open')).toBe('1');
    const collapse = screen.getByRole('button', { name: 'tasks.board.rail.collapse' });
    expect(collapse.getAttribute('aria-expanded')).toBe('true');
    await userEvent.setup().click(collapse);
    expect(window.localStorage.getItem('nf:board-cancelled-open')).toBe('0');
  });

  it('makes the title link the keyboard drag handle, keeping two tab stops per card', () => {
    mockTasks = [
      {
        id: 'task-1',
        title: 'Movable',
        derivedState: 'open',
        priority: 0,
        updatedAt: 0,
        projectId: 'proj-001',
        visibility: 'public',
      },
    ] as TaskListItem[];

    renderWithProviders(<TaskBoardView projectId="proj-001" />);

    const card = screen.getByRole('group', { name: 'Movable' });
    expect(card.getAttribute('tabindex')).toBeNull();
    const handle = screen.getByRole('link', { name: 'Movable' });
    expect(handle.getAttribute('aria-roledescription')).toBeTruthy();
    expect(card.querySelectorAll('a[href], button').length).toBe(2);
  });
});

describe('transitionsByLanding', () => {
  it('picks the first legal verb that reaches each column', () => {
    const open = transitionsByLanding('open');
    expect(open.get('waiting')).toBe('start');
    expect(open.get('done')).toBe('complete');
    expect(open.get('cancelled')).toBe('cancel');
    expect(open.has('open')).toBe(false);
    expect(open.has('review')).toBe(false);
  });

  it('offers only reopen from a closed state', () => {
    expect([...transitionsByLanding('done').entries()].map(([, name]) => name)).toEqual(['reopen']);
  });
});
