import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Popover from './popover';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Popover [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders closed trigger with no a11y violations', async () => {
    const { baseElement } = render(
      <Popover content={<p>panel</p>}>
        <button type="button">Open</button>
      </Popover>,
    );
    expect(screen.getByRole('button', { name: 'Open' })).toBeDefined();
    expect(await axe(baseElement)).toHaveNoViolations();
  });

  it('opens on click and closes on Escape', async () => {
    const user = userEvent.setup();
    render(
      <Popover
        content={
          <div>
            <button type="button">Inside</button>
          </div>
        }
      >
        <button type="button">Open</button>
      </Popover>,
    );
    await user.click(screen.getByRole('button', { name: 'Open' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Inside' })).toBeDefined());
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Inside' })).toBeNull());
  });
});
