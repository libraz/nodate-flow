import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import DatePicker from './date-picker';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('DatePicker [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders trigger with no a11y violations', async () => {
    const { baseElement } = render(
      <DatePicker
        value="2026-06-15"
        onChange={() => undefined}
        prevLabel="Previous month"
        nextLabel="Next month"
      />,
    );
    expect(screen.getByRole('button', { name: '2026-06-15' })).toBeDefined();
    expect(await axe(baseElement)).toHaveNoViolations();
  });

  it('does not render the clear button when onClear is omitted', async () => {
    const user = userEvent.setup();
    render(
      <DatePicker
        value="2026-06-15"
        onChange={() => undefined}
        prevLabel="Previous month"
        nextLabel="Next month"
      />,
    );
    await user.click(screen.getByRole('button', { name: '2026-06-15' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Next month' })).toBeDefined());
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull();
  });

  it('renders clear button and fires onClear, then closes popover', async () => {
    const user = userEvent.setup();
    const onClear = vi.fn();
    render(
      <DatePicker
        value="2026-06-15"
        onChange={() => undefined}
        prevLabel="Previous month"
        nextLabel="Next month"
        onClear={onClear}
        clearLabel="Clear"
      />,
    );
    await user.click(screen.getByRole('button', { name: '2026-06-15' }));
    const clearBtn = await screen.findByRole('button', { name: 'Clear' });
    await user.click(clearBtn);
    expect(onClear).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull());
  });
});
