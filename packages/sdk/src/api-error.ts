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
