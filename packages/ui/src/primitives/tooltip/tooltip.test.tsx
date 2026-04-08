import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Tooltip from './tooltip';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Tooltip [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders trigger with no a11y violations (closed)', async () => {
    const { baseElement } = render(
      <Tooltip content="Help text" delay={0}>
        <button type="button">Help</button>
      </Tooltip>,
    );
    expect(screen.getByRole('button', { name: 'Help' })).toBeDefined();
    expect(await axe(baseElement)).toHaveNoViolations();
  });

  it('opens on focus and shows content', async () => {
    render(
      <Tooltip content="Help text" delay={0}>
        <button type="button">Help</button>
      </Tooltip>,
    );
    screen.getByRole('button').focus();
    await waitFor(() => {
      expect(screen.getByRole('tooltip')).toBeDefined();
    });
    expect(screen.getByText('Help text')).toBeDefined();
  });

  it('dismisses on Escape', async () => {
    const user = userEvent.setup();
    render(
      <Tooltip content="Help text" delay={0}>
        <button type="button">Help</button>
      </Tooltip>,
    );
    screen.getByRole('button').focus();
    await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeNull());
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('tooltip')).toBeNull());
  });
});
