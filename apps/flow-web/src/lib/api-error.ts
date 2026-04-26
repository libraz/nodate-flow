/**
 * API error utilities for flow-web.
 *
 * Re-exports the shared {@link ApiError} class from `@nodate-flow/sdk`
 * so feature code can import error helpers from a single location.
 */

import { ApiError, type ProblemJson, toApiError } from '@nodate-flow/sdk';
import type { TFunction } from 'i18next';

export { ApiError, toApiError };
export type { ProblemJson };

/**
 * Format an unknown error caught from a TanStack Query mutation or query
 * into a user-facing string suitable for display in a toast or alert.
 *
 * Resolution order:
 * 1. {@link ApiError} with a `code` -> translate via the `errors` namespace
 *    (falling back to {@link ApiError.message} if no translation is found).
 * 2. {@link ApiError} without a code, or any other {@link Error} -> the
 *    {@link Error.message} verbatim.
 * 3. Anything else (string, null, ...) -> translate {@code fallbackKey}.
 *
 * @param error - The caught value from a `try/catch` or mutation `onError`.
 * @param t - i18next {@link TFunction} from `useTranslation('common')` (or
 *            any namespace; the helper passes `ns: 'errors'` explicitly).
 * @param fallbackKey - Translation key (current namespace) used when the
 *                      error has no usable message.
 * @returns A localized, ready-to-display string.
 */
export function formatApiError(error: unknown, t: TFunction, fallbackKey: string): string {
  if (error instanceof ApiError) {
    if (error.code) {
      return t(error.code, { ns: 'errors', defaultValue: error.message });
    }
    return error.message;
  }
  if (error instanceof Error) return error.message;
  return t(fallbackKey);
}
