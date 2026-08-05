/**
 * The mode a component is in has to be decided once.
 *
 * Recomputing `value !== undefined` every render let a controlled
 * component slip into uncontrolled for a render — writes went to
 * internal state the parent never saw, and a later undefined resurfaced
 * that stale value as if the parent had asked for it. The tests below
 * pin the latch, and pin that the mismatch is reported instead of
 * quietly worked around.
 */

import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useControllableState } from '../src/hooks/use-controllable-state';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useControllableState', () => {
  it('leaves a controlled value to the parent', () => {
    const onChange = vi.fn();
    const { result, rerender } = renderHook(
      (props: { value: string }) => useControllableState<string>({ ...props, onChange }),
      { initialProps: { value: 'a' } },
    );
    expect(result.current[0]).toBe('a');

    act(() => result.current[1]('b'));
    // The parent has not re-rendered, so the value has not moved.
    expect(result.current[0]).toBe('a');
    expect(onChange).toHaveBeenCalledWith('b');

    rerender({ value: 'b' });
    expect(result.current[0]).toBe('b');
  });

  it('owns the value when only a default is given', () => {
    const { result } = renderHook(() => useControllableState<string>({ defaultValue: 'a' }));
    expect(result.current[0]).toBe('a');
    act(() => result.current[1]('b'));
    expect(result.current[0]).toBe('b');
  });

  it('stays controlled when the value goes undefined for a render', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { result, rerender } = renderHook(
      (props: { value: string | undefined }) => useControllableState<string>(props),
      { initialProps: { value: 'a' as string | undefined } },
    );

    rerender({ value: undefined });
    // Not 'a' from a stale internal copy, and not a value the parent
    // never asked for: undefined means undefined while controlled.
    expect(result.current[0]).toBeUndefined();

    act(() => result.current[1]('b'));
    // A controlled component does not write to internal state, so the
    // next undefined render cannot resurface 'b'.
    rerender({ value: undefined });
    expect(result.current[0]).toBeUndefined();

    rerender({ value: 'c' });
    expect(result.current[0]).toBe('c');
  });

  it('stays uncontrolled when a value appears later', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { result, rerender } = renderHook(
      (props: { value: string | undefined }) =>
        useControllableState<string>({ ...props, defaultValue: 'a' }),
      { initialProps: { value: undefined as string | undefined } },
    );
    act(() => result.current[1]('b'));
    expect(result.current[0]).toBe('b');

    rerender({ value: 'c' });
    expect(result.current[0]).toBe('b');
  });

  it('reports a mode change once, naming the component', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { rerender } = renderHook(
      (props: { value: string | undefined }) =>
        useControllableState<string>({ ...props, name: 'Combobox' }),
      { initialProps: { value: 'a' as string | undefined } },
    );
    rerender({ value: undefined });
    rerender({ value: undefined });

    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0]?.[0]).toContain('Combobox');
    expect(spy.mock.calls[0]?.[0]).toContain('controlled to uncontrolled');
  });

  it('says nothing while the mode holds', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { rerender } = renderHook(
      (props: { value: string }) => useControllableState<string>(props),
      { initialProps: { value: 'a' } },
    );
    rerender({ value: 'b' });
    expect(spy).not.toHaveBeenCalled();
  });
});
