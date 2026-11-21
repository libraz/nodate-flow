import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { formatPickerDate, isPast, isToday } from '../linked-event-time';

const MIDNIGHT_UTC = Date.UTC(2026, 2, 10) / 1000;

describe('all-day link times', () => {
  const originalTz = process.env.TZ;
  beforeEach(() => {
    process.env.TZ = 'America/Los_Angeles';
  });
  afterEach(() => {
    if (originalTz === undefined) delete process.env.TZ;
    else process.env.TZ = originalTz;
  });

  it('names an all-day start by its UTC date, not the browser zone', () => {
    expect(formatPickerDate(MIDNIGHT_UTC, 'en', true)).toBe('Mar 10');
  });

  it('is today on the local day that carries its UTC date', () => {
    const noonLocalMarch10 = new Date(2026, 2, 10, 12).getTime();
    expect(isToday(MIDNIGHT_UTC, noonLocalMarch10, true)).toBe(true);
  });

  it('is not past during its own day', () => {
    const noonLocalMarch10 = new Date(2026, 2, 10, 12).getTime();
    expect(isPast(MIDNIGHT_UTC, noonLocalMarch10, true)).toBe(false);
  });
});
