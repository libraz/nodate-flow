/**
 * @brief State helper that wraps any async submit handler with a
 *        {@code submitting} flag and try/catch/finally lifecycle.
 *
 * Three settings panels (TOTP enroll/disable/regenerate) all maintain
 * an identical "set submitting → call mutation → reset" boilerplate.
 * This hook collapses that pattern so callers only provide the work
 * function and an optional error handler.
 *
 * @return {@code submitting} — `true` while {@code run()} is in flight.
 *         {@code run(fn)} — invokes {@code fn}; resolves to its return
 *         value on success, or {@code undefined} on caught failure.
 */
import { useCallback, useState } from 'react';

export interface PanelSubmission {
  submitting: boolean;
  run: <T>(fn: () => Promise<T>, onError?: (err: unknown) => void) => Promise<T | undefined>;
}

export function usePanelSubmission(): PanelSubmission {
  const [submitting, setSubmitting] = useState(false);

  const run = useCallback(
    async <T>(fn: () => Promise<T>, onError?: (err: unknown) => void): Promise<T | undefined> => {
      setSubmitting(true);
      try {
        return await fn();
      } catch (err) {
        onError?.(err);
        return undefined;
      } finally {
        setSubmitting(false);
      }
    },
    [],
  );

  return { submitting, run };
}
