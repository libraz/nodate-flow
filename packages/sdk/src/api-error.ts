/**
 * Base API error class used across all feature API modules.
 * Captures the error code from the API response envelope and, when
 * available, the originating HTTP status so consumers (e.g. a global
 * QueryCache error handler) can branch on 401 / 403 / 404 without
 * re-parsing the underlying response.
 *
 * The HTTP status is exposed as `httpStatus` rather than `status` so
 * feature-specific subclasses (e.g. `AiProvidersQueryError`) that
 * already carry their own `status: number` field continue to compile
 * without needing an `override` modifier.
 */
import { lookupErrorI18nKey } from './error-lookup.js';

export class ApiError extends Error {
  readonly code: string | undefined;
  readonly httpStatus: number | undefined;
  readonly userAction: string | undefined;
  readonly i18nKey: string | undefined;
  readonly extensions: Record<string, unknown> | undefined;

  constructor(
    code: string | undefined,
    message: string,
    httpStatus?: number,
    userAction?: string,
    i18nKey?: string,
    extensions?: Record<string, unknown>,
  ) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.httpStatus = httpStatus;
    this.userAction = userAction;
    this.i18nKey = i18nKey;
    this.extensions = extensions;
  }
}

/**
 * A request that never became an answer: DNS, TCP, TLS, CORS, an
 * offline device, a cancelled fetch. It is an {@link ApiError} with no
 * code and no status, because there was no server on the other end to
 * supply either.
 *
 * It exists as a type so the UI never has to read the browser's own
 * words to the user. `fetch` rejects with a `TypeError` whose message
 * is written by the engine — "Failed to fetch" in Chrome,
 * "NetworkError when attempting to fetch resource" in Firefox, "Load
 * failed" in Safari — always in English, whatever language the app is
 * running in. Any layer that falls back to `error.message` prints that
 * verbatim, so the conversion has to happen here, at the one place
 * every call leaves through, rather than at the dozens of call sites
 * that would each have to remember.
 *
 * There is deliberately no error code for it. The `errors/*.yaml`
 * catalogue records what a service decided about a request; a request
 * that never arrived was never decided on, and inventing a code would
 * put a client-side condition in a catalogue the server owns.
 *
 * The value the browser threw is kept as `cause`, so a log still shows
 * which transport failure it was, and a caller that needs to tell a
 * cancellation from a dropped connection still can.
 */
export class NetworkError extends ApiError {
  constructor(message: string, cause?: unknown) {
    super(undefined, message);
    this.name = 'NetworkError';
    if (cause !== undefined) this.cause = cause;
  }
}

/**
 * Wraps whatever `fetch` threw into a {@link NetworkError}.
 *
 * `fallback` becomes the message, so the English text carried by the
 * error still describes the operation that failed ("Failed to load
 * tasks") rather than the engine's wording of the transport failure.
 * The UI translates from the type, not from the message.
 */
export function toNetworkError(cause: unknown, fallback: string): NetworkError {
  return new NetworkError(fallback, cause);
}

/**
 * Whether a caught value represents a transport-layer failure rather
 * than a response.
 *
 * Decided from the thrown value alone. `fetch` rejects with a
 * `TypeError` when the request never reached a server (DNS, TCP, CORS,
 * offline) and with a `DOMException` named `AbortError` on
 * cancellation; anything else means we did get an answer, or that
 * something other than the network went wrong on this side.
 *
 * Inferring "network" from a missing HTTP status would be wrong here,
 * because a status is also missing whenever an error envelope fails to
 * parse — that would classify a genuine rejection as a blip and keep a
 * dead session alive.
 */
export function isTransportFailure(err: unknown): boolean {
  if (err instanceof TypeError) return true;
  if (typeof DOMException !== 'undefined' && err instanceof DOMException) {
    return err.name === 'AbortError';
  }
  return false;
}

/**
 * Converts an SDK error response into an ApiError.
 * Extracts `detail`, `title`, and `type` fields from problem+json error
 * envelopes. Pass `httpStatus` when available (e.g. from
 * `Response.status` or the problem+json `status` field) so the resulting
 * error can drive status-based routing such as global 401 handling.
 *
 * `code` and `message` are accepted as fallbacks for `type` and
 * `detail`. Every server emitter writes problem+json — that is asserted
 * on the server side, where it can actually be enforced — so this is
 * insurance for a body that reaches the SDK from somewhere else: a
 * reverse proxy, an old build behind a stale cache, a gateway of its
 * own. Losing the code costs the caller its branch; losing the status
 * costs more, because the terminal-401 handling and the "never retry a
 * 4xx" rule both read it, and an error with no status is treated as a
 * network blip that gets retried against a session that is already dead.
 */
export function toApiError(err: unknown, fallback: string, httpStatus?: number): ApiError {
  if (err && typeof err === 'object') {
    const obj = err as {
      detail?: unknown;
      title?: unknown;
      type?: unknown;
      status?: unknown;
      code?: unknown;
      message?: unknown;
      userAction?: unknown;
      extensions?: unknown;
    };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      (typeof obj.message === 'string' && obj.message) ||
      fallback;
    const code =
      typeof obj.type === 'string' ? obj.type : typeof obj.code === 'string' ? obj.code : undefined;
    const resolvedStatus = httpStatus ?? (typeof obj.status === 'number' ? obj.status : undefined);
    const userAction = typeof obj.userAction === 'string' ? obj.userAction : undefined;
    const extensions =
      obj.extensions && typeof obj.extensions === 'object'
        ? (obj.extensions as Record<string, unknown>)
        : undefined;
    const extensionI18nKey =
      typeof extensions?.['x-i18n-key'] === 'string'
        ? extensions['x-i18n-key']
        : typeof extensions?.i18nKey === 'string'
          ? extensions.i18nKey
          : undefined;
    const i18nKey = extensionI18nKey ?? lookupErrorI18nKey(code);
    return new ApiError(code, message, resolvedStatus, userAction, i18nKey, extensions);
  }
  return new ApiError(undefined, fallback, httpStatus);
}

/** RFC 7807 problem+json shape extracted from SDK error responses. */
export interface ProblemJson {
  type?: string;
  title?: string;
  detail?: string;
  status?: number;
  userAction?: string;
  extensions?: Record<string, unknown>;
}
