/**
 * useSlowPending — reports when a write has been in flight long enough
 * that a disabled button has stopped explaining anything.
 *
 * A greyed-out button says exactly the same thing at 200ms and at 20
 * seconds, and the second of those is indistinguishable from an app that
 * has died. Nothing bounds a request — the SDK sets no timeout and no
 * `AbortSignal` — so the only honest thing a dialog can do is admit,
 * after a while, that it has not heard back.
 *
 * This does not cancel, retry or fail anything. It only decides when the
 * dialog owes the user a sentence.
 */

import { useEffect, useState } from 'react';

/**
 * How long a write may run before the dialog says something.
 *
 * Past the point where a healthy write on a slow connection has already
 * landed, and well short of the patience of someone who has begun to
 * suspect the page is frozen.
 */
export const SLOW_PENDING_MS = 5_000;

/**
 * True once `pending` has been continuously true for `delayMs`. Resets
 * the moment the write settles, so a fast retry starts from silence.
 */
export function useSlowPending(pending: boolean, delayMs: number = SLOW_PENDING_MS): boolean {
  const [slow, setSlow] = useState(false);

  useEffect(() => {
    if (!pending) {
      setSlow(false);
      return;
    }
    const timer = setTimeout(() => {
      setSlow(true);
    }, delayMs);
    return () => {
      clearTimeout(timer);
    };
  }, [pending, delayMs]);

  return slow;
}
