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
export class ApiError extends Error {
  readonly code: string | undefined;
  readonly httpStatus: number | undefined;
  constructor(code: string | undefined, message: string, httpStatus?: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.httpStatus = httpStatus;
  }
}

/**
 * Converts an SDK error response into an ApiError.
 * Extracts `detail`, `title`, and `type` fields from Huma error envelopes.
 * Pass `httpStatus` when available (e.g. from `Response.status` or the
 * problem+json `status` field) so the resulting error can drive
 * status-based routing such as global 401 handling.
 */
export function toApiError(err: unknown, fallback: string, httpStatus?: number): ApiError {
  if (err && typeof err === 'object') {
    const obj = err as {
      detail?: unknown;
      title?: unknown;
      type?: unknown;
      status?: unknown;
    };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    const resolvedStatus = httpStatus ?? (typeof obj.status === 'number' ? obj.status : undefined);
    return new ApiError(code, message, resolvedStatus);
  }
  return new ApiError(undefined, fallback, httpStatus);
}

/** RFC 7807 problem+json shape extracted from SDK error responses. */
export interface ProblemJson {
  type?: string;
  title?: string;
  detail?: string;
  status?: number;
}
