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
import { describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';

import { renderWithProviders } from '@tests/helpers/render';
import type { TaskListItem } from '../api';
import TaskBoardView from '../task-board-view';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useTasksQuery: () => ({ data: [] as TaskListItem[] }),
    useTransitionTask: () => ({ mutate: vi.fn(), isPending: false }),
  };
});

vi.mock('../../projects/api', async () => {
  const actual = await vi.importActual<typeof import('../../projects/api')>('../../projects/api');
  return {
    ...actual,
    useProjectDependenciesQuery: () => ({ data: [] }),
  };
});

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

describe('<TaskBoardView>', () => {
  it('renders a listitem placeholder inside every empty column', () => {
    renderWithProviders(<TaskBoardView projectId="proj-001" />);

    // Each of the five state columns is its own list. With zero tasks,
    // each list must still expose at least one listitem so screen
    // readers (and axe) recognise the empty drop zone as a valid list.
    const lists = screen.getAllByRole('list');
    expect(lists).toHaveLength(5);
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
});
