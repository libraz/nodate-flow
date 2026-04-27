import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { useSubmitGuard } from '../../../lib/use-submit-guard';

describe('useSubmitGuard', () => {
  it('rejects a re-entrant guard() call within the same tick', () => {
    const { result } = renderHook(() => useSubmitGuard());

    let firstReturn = true;
    let secondReturn = false;
    act(() => {
      firstReturn = result.current.guard();
      secondReturn = result.current.guard();
    });

    expect(firstReturn).toBe(false);
    expect(secondReturn).toBe(true);
    expect(result.current.submitting).toBe(true);
  });

  it('allows guard() again after end() releases the lock', () => {
    const { result } = renderHook(() => useSubmitGuard());

    act(() => {
      result.current.guard();
    });
    expect(result.current.submitting).toBe(true);

    act(() => {
      result.current.end();
    });
    expect(result.current.submitting).toBe(false);

    let secondCycle = true;
    act(() => {
      secondCycle = result.current.guard();
    });
    expect(secondCycle).toBe(false);
    expect(result.current.submitting).toBe(true);
  });

  it('begin() locks the guard without going through guard()', () => {
    const { result } = renderHook(() => useSubmitGuard());

    act(() => {
      result.current.begin();
    });
    expect(result.current.submitting).toBe(true);

    let blocked = false;
    act(() => {
      blocked = result.current.guard();
    });
    expect(blocked).toBe(true);
  });

  it('submitting flag toggles from false to true to false across the lifecycle', () => {
    const { result } = renderHook(() => useSubmitGuard());
    expect(result.current.submitting).toBe(false);

    act(() => {
      result.current.guard();
    });
    expect(result.current.submitting).toBe(true);

    act(() => {
      result.current.end();
    });
    expect(result.current.submitting).toBe(false);
  });
});
