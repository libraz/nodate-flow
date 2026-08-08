/**
 * Keyboard reachability of the day grid.
 *
 * These tests drive the picker with the keyboard only — no `user.click` on a
 * day — because the failure they guard against is not a missing attribute but
 * a grid that a keyboard user cannot practically operate: 31 sequential tab
 * stops with no arrow-key movement. Asserting `role="row"` alone would pass
 * against that markup.
 */

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import DatePicker from './date-picker';

const NAV_LABELS = { prevLabel: 'Previous month', nextLabel: 'Next month' } as const;

/**
 * Opens the popover using only the keyboard and returns the user session.
 * `triggerLabel` is the date the trigger shows, asserted on the way through
 * so a mis-set fixture fails here rather than three assertions later.
 */
async function openWithKeyboard(triggerLabel: string): Promise<ReturnType<typeof userEvent.setup>> {
  const user = userEvent.setup();
  await user.tab();
  expect(document.activeElement?.textContent).toBe(triggerLabel);
  await user.keyboard('{Enter}');
  await screen.findByRole('grid');
  return user;
}

/** Every day button currently in the tab order (tabIndex 0). */
function tabbableDays(): HTMLElement[] {
  return screen
    .getAllByRole('gridcell')
    .flatMap((cell) => Array.from(cell.querySelectorAll('button')))
    .filter((btn) => btn.tabIndex === 0);
}

function focusedDayText(): string | null {
  return document.activeElement?.textContent ?? null;
}

describe('DatePicker keyboard grid', () => {
  it('exposes rows of gridcells, not a flat run of buttons', async () => {
    render(<DatePicker value="2026-06-15" onChange={vi.fn()} {...NAV_LABELS} />);
    await openWithKeyboard('2026-06-15');

    const rows = screen.getAllByRole('row');
    expect(rows.length).toBeGreaterThan(1);
    expect(screen.getAllByRole('columnheader')).toHaveLength(7);
    // Every week row is a full seven cells wide, padding included — a short
    // row makes the grid's column semantics meaningless.
    for (const row of rows.slice(1)) {
      expect(row.querySelectorAll('[role="gridcell"]')).toHaveLength(7);
    }
    // June 2026 has 30 days.
    expect(screen.getAllByRole('gridcell').filter((c) => c.textContent !== '')).toHaveLength(30);
  });

  it('offers exactly one tab stop for the whole month', async () => {
    render(<DatePicker value="2026-06-15" onChange={vi.fn()} {...NAV_LABELS} />);
    await openWithKeyboard('2026-06-15');

    const tabbable = tabbableDays();
    expect(tabbable).toHaveLength(1);
    expect(tabbable[0]?.textContent).toBe('15');
  });

  it('reaches the day grid by tabbing past the month nav', async () => {
    render(<DatePicker value="2026-06-15" onChange={vi.fn()} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-06-15');

    // Opening lands on the first control in the panel.
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Previous month');
    await user.tab();
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Next month');
    // One more Tab reaches the calendar itself — not the 1st of the month
    // followed by thirty more stops.
    await user.tab();
    expect(focusedDayText()).toBe('15');
    expect(document.activeElement?.closest('[role="grid"]')).not.toBeNull();
  });

  it('selects a date with arrow keys and Enter, never touching the mouse', async () => {
    const onChange = vi.fn();
    render(<DatePicker value="2026-06-15" onChange={onChange} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-06-15');

    (tabbableDays()[0] as HTMLElement).focus();
    await user.keyboard('{ArrowDown}{ArrowRight}{ArrowRight}');
    expect(focusedDayText()).toBe('24');
    await user.keyboard('{Enter}');
    expect(onChange).toHaveBeenCalledWith('2026-06-24');
  });

  it('walks a whole month in a handful of presses instead of 30 tabs', async () => {
    render(<DatePicker value="2026-06-01" onChange={vi.fn()} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-06-01');
    (tabbableDays()[0] as HTMLElement).focus();
    expect(focusedDayText()).toBe('1');

    // Four ArrowDown presses cover 29 days; the whole month is within five.
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}');
    expect(focusedDayText()).toBe('29');
  });

  it('moves the visible month when an arrow key crosses the boundary', async () => {
    render(<DatePicker value="2026-06-01" onChange={vi.fn()} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-06-01');
    (tabbableDays()[0] as HTMLElement).focus();

    await user.keyboard('{ArrowLeft}');
    expect(screen.getByText('May 2026')).toBeDefined();
    expect(focusedDayText()).toBe('31');
    // Focus stayed inside the grid rather than being dropped on the body.
    expect(document.activeElement?.getAttribute('role')).toBeNull();
    expect(document.activeElement?.closest('[role="grid"]')).not.toBeNull();

    await user.keyboard('{ArrowRight}');
    expect(screen.getByText('June 2026')).toBeDefined();
    expect(focusedDayText()).toBe('1');
  });

  it('pages by month with PageUp / PageDown and by year with Shift', async () => {
    render(<DatePicker value="2026-06-15" onChange={vi.fn()} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-06-15');
    (tabbableDays()[0] as HTMLElement).focus();

    await user.keyboard('{PageDown}');
    expect(screen.getByText('July 2026')).toBeDefined();
    expect(focusedDayText()).toBe('15');

    await user.keyboard('{PageUp}');
    expect(screen.getByText('June 2026')).toBeDefined();

    await user.keyboard('{Shift>}{PageDown}{/Shift}');
    expect(screen.getByText('June 2027')).toBeDefined();
    expect(focusedDayText()).toBe('15');
  });

  it('clamps a month page onto a shorter month', async () => {
    render(<DatePicker value="2026-01-31" onChange={vi.fn()} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-01-31');
    (tabbableDays()[0] as HTMLElement).focus();

    await user.keyboard('{PageDown}');
    expect(screen.getByText('February 2026')).toBeDefined();
    expect(focusedDayText()).toBe('28');
  });

  it('moves to the ends of the week row with Home and End', async () => {
    // 2026-06-15 is a Monday; the week starts on Monday by default.
    render(<DatePicker value="2026-06-17" onChange={vi.fn()} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-06-17');
    (tabbableDays()[0] as HTMLElement).focus();

    await user.keyboard('{Home}');
    expect(focusedDayText()).toBe('15');
    await user.keyboard('{End}');
    expect(focusedDayText()).toBe('21');
  });

  it('keeps a min-date-blocked day focusable but unselectable', async () => {
    const onChange = vi.fn();
    render(
      <DatePicker value="2026-06-15" onChange={onChange} minDate="2026-06-10" {...NAV_LABELS} />,
    );
    const user = await openWithKeyboard('2026-06-15');
    (tabbableDays()[0] as HTMLElement).focus();

    // Walk back past the min date: a `disabled` button would refuse focus and
    // strand the user with no way back into the grid.
    await user.keyboard('{ArrowUp}{ArrowUp}');
    expect(focusedDayText()).toBe('1');
    expect(document.activeElement?.getAttribute('aria-disabled')).toBe('true');

    await user.keyboard('{Enter}');
    expect(onChange).not.toHaveBeenCalled();

    await user.keyboard('{ArrowDown}{ArrowDown}');
    expect(focusedDayText()).toBe('15');
    await user.keyboard('{Enter}');
    expect(onChange).toHaveBeenCalledWith('2026-06-15');
  });

  it('does not hijack modified arrow keys', async () => {
    render(<DatePicker value="2026-06-15" onChange={vi.fn()} {...NAV_LABELS} />);
    const user = await openWithKeyboard('2026-06-15');
    (tabbableDays()[0] as HTMLElement).focus();

    await user.keyboard('{Control>}{ArrowRight}{/Control}');
    expect(focusedDayText()).toBe('15');
  });

  it('has no axe violations with the grid open', async () => {
    const { baseElement } = render(
      <DatePicker
        value="2026-06-15"
        onChange={vi.fn()}
        onClear={vi.fn()}
        clearLabel="Clear"
        {...NAV_LABELS}
      />,
    );
    await openWithKeyboard('2026-06-15');
    expect(await axe(baseElement)).toHaveNoViolations();
  });
});
