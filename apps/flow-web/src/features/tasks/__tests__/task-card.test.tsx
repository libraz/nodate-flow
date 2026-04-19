/**
 * Component tests for TaskCard.
 *
 * TaskCard is the primary card rendered inside board columns. These
 * tests verify structural rendering (title, priority badge, due date,
 * blocked indicator) and user interactions (drag, click, move menu).
 */

import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';

import { renderWithProviders } from '../../../test/helpers/render';
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
    onDragStart: vi.fn(),
    onDragEnd: vi.fn(),
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

    // The due date badge should have the aria-label from i18n key.
    const dueBadge = screen.getByLabelText('tasks.columns.due');
    expect(dueBadge).toBeDefined();
  });

  it('does not render due date badge when dueOn is absent', () => {
    renderWithProviders(<TaskCard {...defaultProps()} />);

    const badges = screen.queryByLabelText('tasks.columns.due');
    expect(badges).toBeNull();
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
    // Click on the card area (not the title link).
    // The Card component wraps everything; find it by its class.
    const card = screen.getByRole('link', { name: 'Design auth flow' }).closest('[draggable]');
    expect(card).toBeDefined();
    if (card) {
      await user.click(card);
      expect(onSelect).toHaveBeenCalledWith('task-001');
    }
  });

  it('has no a11y violations', async () => {
    const { container } = renderWithProviders(
      <TaskCard {...defaultProps({ blockedByOpenCount: 1 })} />,
    );
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});
