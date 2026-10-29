import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it } from 'vitest';

import TaskViewSwitcher from '../task-view-switcher';
import { setTaskView, useTaskView } from '../use-task-view';

function Probe() {
  return <output data-testid="view">{useTaskView()}</output>;
}

describe('TaskViewSwitcher', () => {
  beforeEach(() => {
    setTaskView('board');
  });

  it('marks the active view and switches on click', async () => {
    const user = userEvent.setup();
    render(
      <>
        <TaskViewSwitcher />
        <Probe />
      </>,
    );
    const radios = screen.getAllByRole('radio');
    expect(radios).toHaveLength(4);
    expect(radios[0]?.getAttribute('aria-checked')).toBe('true');

    await user.click(radios[1] as HTMLElement);
    expect(screen.getByTestId('view').textContent).toBe('list');
  });

  it('moves the selection with the arrow keys', async () => {
    const user = userEvent.setup();
    render(
      <>
        <TaskViewSwitcher />
        <Probe />
      </>,
    );
    screen.getAllByRole('radio')[0]?.focus();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByTestId('view').textContent).toBe('list');
  });
});
