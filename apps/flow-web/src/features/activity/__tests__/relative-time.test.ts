/**
 * Unit tests for the activity-feed relative-time helper.
 *
 * occurredAt is unix SECONDS. Minor future skew must clamp to "now" so a
 * just-emitted event never renders as "in 1 second".
 */

import { afterEach, describe, expect, it, vi } from 'vitest';

import { formatAbsolute, formatRelative } from '../relative-time';

const NOW_SEC = 1_700_000_000;

afterEach(() => {
  vi.useRealTimers();
});

function freezeNow(): void {
  vi.useFakeTimers();
  vi.setSystemTime(NOW_SEC * 1000);
}

describe('formatRelative', () => {
  it('renders a past minute relative to now', () => {
    freezeNow();
    const out = formatRelative(NOW_SEC - 120, 'en');
    expect(out).toContain('2');
    expect(out.toLowerCase()).toContain('minute');
  });

  it('clamps minor future skew to "now" rather than "in 1 second"', () => {
    freezeNow();
    const out = formatRelative(NOW_SEC + 5, 'en');
    expect(out.toLowerCase()).not.toContain('in ');
  });

  it('steps up to days for older events', () => {
    freezeNow();
    const out = formatRelative(NOW_SEC - 3 * 86_400, 'en');
    expect(out.toLowerCase()).toContain('day');
  });
});

describe('formatAbsolute', () => {
  it('produces a non-empty locale string', () => {
    const out = formatAbsolute(NOW_SEC, 'en');
    expect(out.length).toBeGreaterThan(0);
  });
});
