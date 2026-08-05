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
  /**
   * A date set from outside the calendar has to bring the calendar with
   * it. The previous value was held in a `useMemo` keyed on the value
   * itself, so the comparison was always false and the sync never ran —
   * the picker stayed on whatever month it was showing and the selected
   * day was off screen.
   */
  it('moves the visible month when the value changes from outside', async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <DatePicker
        value="2026-06-15"
        onChange={() => undefined}
        prevLabel="Previous month"
        nextLabel="Next month"
      />,
    );
    await user.click(screen.getByRole('button', { name: '2026-06-15' }));
    expect(screen.getByText('June 2026')).toBeDefined();

    rerender(
      <DatePicker
        value="2026-09-02"
        onChange={() => undefined}
        prevLabel="Previous month"
        nextLabel="Next month"
      />,
    );
    expect(screen.getByText('September 2026')).toBeDefined();
    expect(screen.queryByText('June 2026')).toBeNull();
  });

  it('leaves the month alone while the user is browsing it', async () => {
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
    await user.click(screen.getByRole('button', { name: 'Next month' }));
    expect(screen.getByText('July 2026')).toBeDefined();
  });
});
