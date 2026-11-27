import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createInvalidationCoalescer } from '../invalidation-coalescer';

describe('createInvalidationCoalescer', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('dedupes keys and flushes once after the trailing window', () => {
    const flush = vi.fn();
    const c = createInvalidationCoalescer({ flush });
    c.add([['tasks'], ['projects']]);
    c.add([['tasks'], ['labels', 'a']]);
    vi.advanceTimersByTime(749);
    expect(flush).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(flush).toHaveBeenCalledTimes(1);
    expect(flush).toHaveBeenCalledWith([['tasks'], ['projects'], ['labels', 'a']]);
  });

  it('flushes at the max wait under a steady stream', () => {
    const flush = vi.fn();
    const c = createInvalidationCoalescer({ flush });
    for (let i = 0; i < 4; i++) {
      c.add([['tasks']]);
      vi.advanceTimersByTime(500);
    }
    expect(flush).toHaveBeenCalledTimes(1);
  });

  it('starts a fresh batch after a flush', () => {
    const flush = vi.fn();
    const c = createInvalidationCoalescer({ flush });
    c.add([['a']]);
    vi.advanceTimersByTime(750);
    c.add([['a']]);
    vi.advanceTimersByTime(750);
    expect(flush).toHaveBeenCalledTimes(2);
  });

  it('drops pending keys on dispose', () => {
    const flush = vi.fn();
    const c = createInvalidationCoalescer({ flush });
    c.add([['a']]);
    c.dispose();
    vi.advanceTimersByTime(5_000);
    expect(flush).not.toHaveBeenCalled();
  });
});
