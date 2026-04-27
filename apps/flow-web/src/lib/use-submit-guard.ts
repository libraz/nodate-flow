/**
 * useSubmitGuard — defense-in-depth re-entrancy guard for form submit handlers.
 *
 * The visible `disabled={submitting}` on a submit button only protects after
 * React has flushed the next render. Between the moment a handler is invoked
 * and the next paint, rapid Enter-key spamming or double clicks can fire the
 * same mutation twice. This hook backs the submitting flag with a `useRef`
 * that flips synchronously, so the second `guard()` in the same tick returns
 * `true` and the caller bails before reaching the network call.
 *
 * Typical usage:
 *
 * @example
 * ```tsx
 * const { guard, submitting, end } = useSubmitGuard();
 *
 * const handleSubmit = async (e: FormEvent) => {
 *   e.preventDefault();
 *   if (guard()) return;        // already submitting — bail
 *   try {
 *     await mutation.mutateAsync(payload);
 *   } finally {
 *     end();                    // re-arm the guard
 *   }
 * };
 *
 * return <Button type="submit" disabled={submitting}>{t('save')}</Button>;
 * ```
 */

import { useCallback, useRef, useState } from 'react';

/** Shape returned by {@link useSubmitGuard}. */
export interface SubmitGuard {
  /**
   * Reject re-entrant submits. Returns `true` if a submit is already in flight
   * (caller should `return` immediately); otherwise flips the internal flag to
   * busy and returns `false`. Both the ref and the React state are updated, so
   * callers may also read {@link SubmitGuard.submitting} for UI gating.
   */
  guard: () => boolean;
  /** True while a submit is in flight, suitable for `disabled` props. */
  submitting: boolean;
  /**
   * Mark the guard as busy without going through {@link SubmitGuard.guard}.
   * Useful when a handler kicks off work conditionally and you want to lock
   * in the busy state at a different point than the entry check.
   */
  begin: () => void;
  /**
   * Release the guard. Always call this in a `finally` block so a thrown
   * mutation (or an early `return`) does not leave the form permanently
   * disabled.
   */
  end: () => void;
}

/**
 * Create a submit guard that resists re-entrant invocation within a single
 * render tick. See the file-level docstring for usage.
 */
export function useSubmitGuard(): SubmitGuard {
  const inFlightRef = useRef(false);
  const [submitting, setSubmitting] = useState(false);

  const guard = useCallback((): boolean => {
    if (inFlightRef.current) return true;
    inFlightRef.current = true;
    setSubmitting(true);
    return false;
  }, []);

  const begin = useCallback((): void => {
    inFlightRef.current = true;
    setSubmitting(true);
  }, []);

  const end = useCallback((): void => {
    inFlightRef.current = false;
    setSubmitting(false);
  }, []);

  return { guard, submitting, begin, end };
}
