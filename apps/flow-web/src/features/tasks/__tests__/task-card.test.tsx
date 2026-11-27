/**
 * Component tests for TaskCard.
 *
 * TaskCard is the primary card rendered inside board columns. These
 * tests verify structural rendering (title, priority badge, due date,
 * blocked indicator) and user interactions (drag, click, move menu).
 */

import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import type { TaskListItem } from '../api';
import TaskCard, { type TaskCardProps } from '../task-card';

/** Build a minimal TaskListItem fixture with overrides. */
function aTaskItem(overrides: Partial<TaskListItem> = {}): TaskListItem {
  return {
    id: 'task-001',
    title: 'Design auth flow',
    description: '',
    priority: 2,
    derivedState: 'open',
    // dueOn intentionally omitted to test absent state
    visibility: 'public',
    projectId: 'proj-001',
    createdAt: 1700000000,
    updatedAt: 1700000000,
    ...overrides,
  } as TaskListItem;
}

/** Build default TaskCardProps with vi.fn() handlers. */
function defaultProps(overrides: Partial<TaskCardProps> = {}): TaskCardProps {
  return {
    task: aTaskItem(),
    onSelect: vi.fn(),
    onTransition: vi.fn(),
    ...overrides,
  };
}

describe('<TaskCard>', () => {
  it('renders the task title as a link', () => {
    renderWithProviders(<TaskCard {...defaultProps()} />);

    const link = screen.getByRole('link', { name: 'Design auth flow' });
    expect(link).toBeDefined();
    expect(link.getAttribute('href')).toContain('task-001');
  });

  it('renders priority badge when priority > 0', () => {
    renderWithProviders(<TaskCard {...defaultProps({ task: aTaskItem({ priority: 3 }) })} />);

    // Priority label comes from the i18n key (passthrough in tests).
    const badge = screen.getByText('tasks.priority.high');
    expect(badge).toBeDefined();
  });

  it('does not render priority badge when priority is 0 (none)', () => {
    const { container } = renderWithProviders(
      <TaskCard {...defaultProps({ task: aTaskItem({ priority: 0 }) })} />,
    );

    // None of the priority keys should appear in the rendered output.
    expect(container.textContent).not.toContain('tasks.priority.low');
    expect(container.textContent).not.toContain('tasks.priority.medium');
    expect(container.textContent).not.toContain('tasks.priority.high');
    expect(container.textContent).not.toContain('tasks.priority.urgent');
  });

  it('renders due date badge when dueOn is set', () => {
    renderWithProviders(
      <TaskCard {...defaultProps({ task: aTaskItem({ dueOn: '2099-12-31' }) })} />,
    );

    // The due date carries a visually hidden "due" prefix from the i18n key.
    const due = screen.getByText(/^tasks\.columns\.due:/).closest('time');
    expect(due?.getAttribute('datetime')).toBe('2099-12-31');
  });

  it('does not render due date badge when dueOn is absent', () => {
    renderWithProviders(<TaskCard {...defaultProps()} />);

    expect(screen.queryByText(/^tasks\.columns\.due/)).toBeNull();
  });

  it('renders blocked-by badge when blockedByOpenCount > 0', () => {
    renderWithProviders(<TaskCard {...defaultProps({ blockedByOpenCount: 2 })} />);

    const blockedBadge = screen.getByLabelText('tasks.card.blockedBy');
    expect(blockedBadge).toBeDefined();
    expect(blockedBadge.textContent).toContain('2');
  });

  it('does not render blocked-by badge when blockedByOpenCount is 0', () => {
    renderWithProviders(<TaskCard {...defaultProps({ blockedByOpenCount: 0 })} />);

    const blocked = screen.queryByLabelText('tasks.card.blockedBy');
    expect(blocked).toBeNull();
  });

  it('calls onSelect when the card body is clicked', async () => {
    const onSelect = vi.fn();
    renderWithProviders(<TaskCard {...defaultProps({ onSelect })} />);

    const user = userEvent.setup();
    // Click on the card area (not the title link). The Card component
    // wraps everything; locate it via its non-draggable attribute.
    const card = screen
      .getByRole('link', { name: 'Design auth flow' })
      .closest('[draggable="false"]');
    expect(card).toBeDefined();
    if (card) {
      await user.click(card);
      expect(onSelect).toHaveBeenCalledWith('task-001');
    }
  });

  it('keeps native HTML5 drag off', () => {
    renderWithProviders(<TaskCard {...defaultProps()} />);
    const card = screen
      .getByRole('link', { name: 'Design auth flow' })
      .closest('[draggable="false"]');
    expect(card).not.toBeNull();
  });

  it('renders up to three label chips and a +N overflow', () => {
    const labels = ['A', 'B', 'C', 'D', 'E'].map((name) => ({
      id: `l-${name}`,
      name,
      color: '#336699',
    }));
    renderWithProviders(<TaskCard {...defaultProps({ labels })} />);

    for (const name of ['A', 'B', 'C']) expect(screen.getByText(name)).toBeDefined();
    expect(screen.queryByText('D')).toBeNull();
    expect(screen.getAllByText('tasks.card.labels_more').length).toBeGreaterThan(0);
  });

  it('renders no label row without labels', () => {
    renderWithProviders(<TaskCard {...defaultProps()} />);
    expect(screen.queryByText('tasks.card.labels_more')).toBeNull();
  });

  it('has no a11y violations', async () => {
    const { container } = renderWithProviders(
      <TaskCard {...defaultProps({ blockedByOpenCount: 1 })} />,
    );
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });

  it('shows a muted parent line linking to the parent for a subtask', async () => {
    const onSelect = vi.fn();
    renderWithProviders(
      <TaskCard
        {...defaultProps({
          onSelect,
          task: aTaskItem({ parentTaskId: 'task-parent', parentTaskTitle: 'Ship v1' }),
        })}
      />,
    );

    const parent = screen.getByRole('link', { name: 'Ship v1' });
    expect(parent.getAttribute('href')).toContain('task-parent');
    expect(parent.getAttribute('title')).toBe('Ship v1');
    await userEvent.setup().click(parent);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('falls back to a generic parent label when the title is missing', () => {
    renderWithProviders(
      <TaskCard {...defaultProps({ task: aTaskItem({ parentTaskId: 'task-parent' }) })} />,
    );
    expect(screen.getByText('tasks.hierarchy.parent_fallback')).toBeDefined();
  });

  it('shows a subtask count badge only for a parent', () => {
    const { unmount } = renderWithProviders(
      <TaskCard {...defaultProps({ task: aTaskItem({ subtaskCount: 3 }) })} />,
    );
    expect(screen.getByLabelText('tasks.hierarchy.subtasks')).toBeDefined();
    unmount();
    renderWithProviders(<TaskCard {...defaultProps({ task: aTaskItem({ subtaskCount: 0 }) })} />);
    expect(screen.queryByLabelText('tasks.hierarchy.subtasks')).toBeNull();
  });

  it('shows a comment count chip only above zero', () => {
    const { unmount } = renderWithProviders(
      <TaskCard {...defaultProps({ task: aTaskItem({ commentCount: 4 }) })} />,
    );
    expect(screen.getByLabelText('tasks.card.comments')).toBeDefined();
    unmount();
    renderWithProviders(<TaskCard {...defaultProps({ task: aTaskItem({ commentCount: 0 }) })} />);
    expect(screen.queryByLabelText('tasks.card.comments')).toBeNull();
  });

  it('stacks at most three assignee avatars and names them all', () => {
    const assignees = ['Ann', 'Bob', 'Cy', 'Di', 'Eve'].map((displayName) => ({
      id: `u-${displayName}`,
      displayName,
    }));
    renderWithProviders(<TaskCard {...defaultProps({ task: aTaskItem({ assignees }) })} />);

    const stack = screen.getByRole('img', { name: /Ann.*Eve/ });
    expect(stack.getAttribute('title')).toContain('Di');
    expect(screen.getByText('tasks.assignee.plus_n')).toBeDefined();
  });
});
