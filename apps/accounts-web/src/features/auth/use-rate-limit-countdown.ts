/**
 * @brief Drives the login rate-limit banner countdown.
 *
 * The auth-api signals 429 with a `Retry-After` header carrying the
 * remaining cooldown in seconds. The login form takes that value and
 * feeds it to this hook, which decrements once per second until the
 * cooldown elapses. While `secondsLeft > 0`, the form must disable
 * inputs and the submit button so retries cannot accidentally bump
 * the rate-limit window again.
 *
 * Implemented as a hook (rather than inline inside the page) so the
 * counting logic can be tested in isolation against fake timers.
 */

import { useEffect, useState } from 'react';

export interface UseRateLimitCountdownOptions {
  /** Initial cooldown in seconds. Use `0` (or negative) to clear. */
  seconds: number;
  /**
   * Optional callback fired when the countdown reaches zero. Useful when
   * the consumer wants to clear server-side error state at the same
   * moment the inputs become re-enabled.
   */
  onExpire?: () => void;
}

export interface RateLimitCountdown {
  /** Remaining seconds (>= 0). Zero means the cooldown has elapsed. */
  secondsLeft: number;
  /** True while the cooldown is active (`secondsLeft > 0`). */
  active: boolean;
}

/**
 * @brief Decrement the seconds-left counter once per second until zero.
 *
 * The interval is created on mount / when `seconds` increases and torn
 * down both on cleanup and once the count reaches zero. Re-running the
 * effect when `seconds` decreases below the current `secondsLeft`
 * value is intentional: we always honour the most recent server signal.
 */
export function useRateLimitCountdown(options: UseRateLimitCountdownOptions): RateLimitCountdown {
  const { seconds, onExpire } = options;
  const [secondsLeft, setSecondsLeft] = useState(() => Math.max(0, Math.floor(seconds)));

  // Mirror the latest external `seconds` into local state. We compare
  // first so a steady-state render does not reset the in-progress tick.
  useEffect(() => {
    const sanitised = Math.max(0, Math.floor(seconds));
    setSecondsLeft((prev) => (prev === sanitised ? prev : sanitised));
  }, [seconds]);

  useEffect(() => {
    if (secondsLeft <= 0) return;
    const id = window.setInterval(() => {
      setSecondsLeft((prev) => {
        if (prev <= 1) {
          window.clearInterval(id);
          onExpire?.();
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
    return (): void => {
      window.clearInterval(id);
    };
  }, [secondsLeft, onExpire]);

  return { secondsLeft, active: secondsLeft > 0 };
}
