/**
 * API error utilities for flow-web.
 *
 * Re-exports the shared {@link ApiError} class from `@nodate-flow/sdk`
 * so feature code can import error helpers from a single location.
 */

import { ApiError, NetworkError, type ProblemJson, toApiError } from '@nodate-flow/sdk';
import type { TFunction } from 'i18next';

export type { ProblemJson };
export { ApiError, NetworkError, toApiError };

/**
 * The sentence for a request that never reached a server. Held here
 * rather than at the call sites because the reader's next move is the
 * same wherever it happened — look at their connection, try again —
 * and it has nothing to do with what they were trying to do.
 */
const networkErrorKey = 'common.network_error';

/**
 * Format an unknown error caught from a TanStack Query mutation or query
 * into a user-facing string suitable for display in a toast or alert.
 *
 * Resolution order:
 * 1. {@link ApiError} with a `code` -> translate via the `errors` namespace
 *    (falling back to {@link ApiError.message} if no translation is found).
 *    This is the path almost every refusal takes.
 * 2. A transport failure ({@link isNetworkError}) -> the shared "could not
 *    reach the server" sentence, which is the one case where retrying is
 *    the reader's own move.
 * 3. {@link ApiError} without a code -> translate {@code fallbackKey}. The
 *    server answered but said nothing readable — a bodyless 502, chi's 405
 *    for an unregistered method — so `message` here is the English literal
 *    the call site handed the requester, never a server's words. The
 *    call site's key says what failed, in the reader's language.
 * 4. Any other {@link Error} -> the {@link Error.message} verbatim; a
 *    non-API error thrown inside the app carries its own wording.
 * 5. Anything else (string, null, ...) -> translate {@code fallbackKey}.
 *
 * @param error - The caught value from a `try/catch` or mutation `onError`.
 * @param t - i18next {@link TFunction} from `useTranslation('common')` (or
 *            any namespace; the helper passes `ns` explicitly where it
 *            needs one).
 * @param fallbackKey - Translation key (current namespace) naming the
 *                      operation that failed, used when the error carries
 *                      nothing the reader could act on.
 * @returns A localized, ready-to-display string.
 */
export function formatApiError(error: unknown, t: TFunction, fallbackKey: string): string {
  if (error instanceof ApiError && error.code) {
    return t(error.code, { ns: 'errors', defaultValue: error.message });
  }
  if (isNetworkError(error)) return t(networkErrorKey, { ns: 'common' });
  if (error instanceof ApiError) return t(fallbackKey);
  if (error instanceof Error) return error.message;
  return t(fallbackKey);
}

/**
 * isNetworkError — true when the caught value represents a transport-layer
 * failure (DNS / TCP / CORS / aborted fetch) rather than a server response
 * we could decode. Lets call sites surface a network-specific message
 * ("check your connection") separately from server-returned codes
 * ("invite expired").
 *
 * Heuristics, in order:
 *   - `NetworkError` — what the requester converts a transport failure
 *     into, so every call that goes through `lib/api` lands here.
 *   - Native `TypeError` from `fetch()` (browsers throw this when the
 *     request never reached a server), for the paths that hold their own
 *     fetch rather than the requester.
 *   - `ApiError` with no `code` AND no `httpStatus` — toApiError() only
 *     populates those when it had a parseable response envelope, so the
 *     absence of both signals "we never got a useful reply".
 *   - `DOMException` with name `AbortError` — request cancellation.
 */
export function isNetworkError(error: unknown): boolean {
  if (error instanceof NetworkError) return true;
  if (error instanceof TypeError) return true;
  if (error instanceof DOMException && error.name === 'AbortError') return true;
  if (error instanceof ApiError) {
    return error.code === undefined && error.httpStatus === undefined;
  }
  return false;
}
