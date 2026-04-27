import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { usePanelSubmission } from '../use-panel-submission';

describe('usePanelSubmission', () => {
  it('starts with submitting=false', () => {
    const { result } = renderHook(() => usePanelSubmission());
    expect(result.current.submitting).toBe(false);
  });

  it('resolves with the wrapped function value on success', async () => {
    const { result } = renderHook(() => usePanelSubmission());
    let value: number | undefined;
    await act(async () => {
      value = await result.current.run(() => Promise.resolve(42));
    });
    expect(value).toBe(42);
    expect(result.current.submitting).toBe(false);
  });

  it('invokes onError and returns undefined on failure', async () => {
    const { result } = renderHook(() => usePanelSubmission());
    const err = new Error('boom');
    const onError = vi.fn();
    let value: unknown = 'sentinel';
    await act(async () => {
      value = await result.current.run(() => Promise.reject(err), onError);
    });
    expect(value).toBeUndefined();
    expect(onError).toHaveBeenCalledWith(err);
    expect(result.current.submitting).toBe(false);
  });

  it('flips submitting to true while in flight', async () => {
    const { result } = renderHook(() => usePanelSubmission());
    let resolveFn: (v: string) => void = () => undefined;
    const promise = new Promise<string>((resolve) => {
      resolveFn = resolve;
    });
    let runPromise: Promise<string | undefined> = Promise.resolve(undefined);
    act(() => {
      runPromise = result.current.run(() => promise);
    });
    expect(result.current.submitting).toBe(true);
    await act(async () => {
      resolveFn('done');
      await runPromise;
    });
    expect(result.current.submitting).toBe(false);
  });
});
