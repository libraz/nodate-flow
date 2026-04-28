/**
 * useRateLimitCountdown — verifies the per-second decrement, the
 * `onExpire` callback, and the steady-state behaviour when the input
 * `seconds` value is reset to zero.
 */

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useRateLimitCountdown } from '../use-rate-limit-countdown';

describe('useRateLimitCountdown', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns inactive when initial seconds is zero', () => {
    const { result } = renderHook(() => useRateLimitCountdown({ seconds: 0 }));
    expect(result.current.secondsLeft).toBe(0);
    expect(result.current.active).toBe(false);
  });

  it('decrements by one each second until it reaches zero', () => {
    const onExpire = vi.fn();
    const { result } = renderHook(() => useRateLimitCountdown({ seconds: 3, onExpire }));

    expect(result.current.secondsLeft).toBe(3);
    expect(result.current.active).toBe(true);

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current.secondsLeft).toBe(2);

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current.secondsLeft).toBe(1);

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current.secondsLeft).toBe(0);
    expect(result.current.active).toBe(false);
    expect(onExpire).toHaveBeenCalledTimes(1);
  });

  it('honours an updated `seconds` input', () => {
    const { result, rerender } = renderHook(
      ({ seconds }: { seconds: number }) => useRateLimitCountdown({ seconds }),
      { initialProps: { seconds: 5 } },
    );

    expect(result.current.secondsLeft).toBe(5);

    // Simulate the consumer clearing the cooldown after the API returned
    // a non-rate-limited response.
    rerender({ seconds: 0 });
    expect(result.current.secondsLeft).toBe(0);
    expect(result.current.active).toBe(false);
  });

  it('floors fractional inputs', () => {
    const { result } = renderHook(() => useRateLimitCountdown({ seconds: 2.9 }));
    expect(result.current.secondsLeft).toBe(2);
  });
});
