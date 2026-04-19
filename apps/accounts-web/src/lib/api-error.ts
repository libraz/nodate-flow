/**
 * Base API error class used across all feature API modules.
 * Captures the error code from the API response envelope.
 */
export class ApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
  }
}

/**
 * Converts an SDK error response into an ApiError.
 * Extracts `detail`, `title`, and `type` fields from Huma error envelopes.
 */
export function toApiError(err: unknown, fallback: string): ApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new ApiError(code, message);
  }
  return new ApiError(undefined, fallback);
}

/** RFC 7807 problem+json shape extracted from SDK error responses. */
export interface ProblemJson {
  type?: string;
  title?: string;
  detail?: string;
  status?: number;
}
