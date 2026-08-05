/**
 * DatePicker grid layout for each first-day-of-week the product offers.
 *
 * The prop used to be `'sunday' | 'monday'`, so an account whose stored
 * preference was Saturday had no way to say so — the picker rendered
 * somebody else's week while the calendar grid rendered theirs.
 */

import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it } from 'vitest';

import DatePicker, { type WeekStartDay } from '../date-picker';

/** Open the picker and read the weekday header, left to right. */
async function openOn(weekStart: WeekStartDay): Promise<string[]> {
  const user = userEvent.setup();
  render(
    <DatePicker
      // A month whose 1st is a Wednesday, so each anchor produces a
      // different number of leading blanks.
      value="2026-04-15"
      onChange={() => {}}
      weekStart={weekStart}
      prevLabel="prev"
      nextLabel="next"
    />,
  );
  await user.click(screen.getByRole('button', { name: /2026|15/ }));
  const row = document.querySelector('[class*="weekdays"]');
  if (!row) throw new Error('weekday header not rendered');
  return [...row.children].map((n) => n.textContent ?? '');
}

afterEach(cleanup);

describe('DatePicker weekStart', () => {
  it('starts the header on Monday', async () => {
    expect((await openOn('monday'))[0]).toBe('Mo');
  });

  it('starts the header on Sunday', async () => {
    expect((await openOn('sunday'))[0]).toBe('Su');
  });

  it('starts the header on Saturday', async () => {
    const labels = await openOn('saturday');
    expect(labels[0]).toBe('Sa');
    // The whole week rotates, not just the first cell.
    expect(labels.slice(0, 3)).toEqual(['Sa', 'Su', 'Mo']);
  });

  it('rotates through all seven days exactly once for every anchor', async () => {
    for (const anchor of ['monday', 'sunday', 'saturday'] as const) {
      const labels = await openOn(anchor);
      expect(labels).toHaveLength(7);
      expect(new Set(labels).size).toBe(7);
      cleanup();
    }
  });
});
