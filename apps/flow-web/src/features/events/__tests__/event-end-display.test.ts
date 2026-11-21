import { describe, expect, it } from 'vitest';

import { formatEventEnd } from '../event-detail-page';

const day = (d: number): number => Date.UTC(2026, 2, d) / 1000;

describe('formatEventEnd', () => {
  it('shows the last day of an all-day event, not its exclusive end', () => {
    expect(formatEventEnd(day(10), day(13), 'en', true)).toBe('Mar 12, 2026');
  });

  it('shows a one-day all-day event ending on its start day', () => {
    expect(formatEventEnd(day(10), day(11), 'en', true)).toBe('Mar 10, 2026');
  });
});
