import { describe, expect, it } from 'vitest';

import { AVAILABILITY_MARK, combineAvailability, getAvailability } from '../availability';
import type { Availability, Flexibility, ShowAs } from '../types';

const FLEXIBILITIES: Flexibility[] = ['fixed', 'negotiable', 'conditional'];

describe('getAvailability', () => {
  it('reports a free slot as open regardless of flexibility', () => {
    // Marking free time movable says nothing — there is nothing to move.
    for (const flexibility of FLEXIBILITIES) {
      expect(getAvailability('free', flexibility)).toBe('open');
    }
  });

  it('reports a tentative slot as tentative regardless of flexibility', () => {
    for (const flexibility of FLEXIBILITIES) {
      expect(getAvailability('tentative', flexibility)).toBe('tentative');
    }
  });

  it('separates a movable commitment from a fixed one', () => {
    // This is the distinction the column exists for: both are busy, but
    // only one is worth asking about.
    expect(getAvailability('busy', 'fixed')).toBe('blocked');
    expect(getAvailability('busy', 'negotiable')).toBe('negotiable');
    expect(getAvailability('busy', 'conditional')).toBe('negotiable');
  });

  it('treats out-of-office the same as busy', () => {
    expect(getAvailability('oof', 'fixed')).toBe('blocked');
    expect(getAvailability('oof', 'negotiable')).toBe('negotiable');
  });

  it('never leaves a combination underived', () => {
    const showAsValues: ShowAs[] = ['busy', 'free', 'tentative', 'oof'];
    for (const showAs of showAsValues) {
      for (const flexibility of FLEXIBILITIES) {
        expect(AVAILABILITY_MARK[getAvailability(showAs, flexibility)]).toBeTruthy();
      }
    }
  });
});

describe('combineAvailability', () => {
  it('treats an empty span as open', () => {
    expect(combineAvailability([])).toBe('open');
  });

  it('lets the worst slot decide', () => {
    // A second, free slot cannot rescue a span that is already blocked.
    expect(combineAvailability(['open', 'blocked'])).toBe('blocked');
    expect(combineAvailability(['open', 'tentative'])).toBe('tentative');
    expect(combineAvailability(['negotiable', 'tentative'])).toBe('negotiable');
    expect(combineAvailability(['blocked', 'negotiable'])).toBe('blocked');
  });

  it('does not depend on order', () => {
    const slots: Availability[] = ['tentative', 'blocked', 'open', 'negotiable'];
    expect(combineAvailability(slots)).toBe(combineAvailability([...slots].reverse()));
  });
});
