import { toApiError } from '@nodate-flow/sdk/api-error';
import { requestFailed, type SdkResult } from '@nodate-flow/sdk/request';

import { EXIT_AUTH, EXIT_RUNTIME, type ExitCode } from './exit.js';

/**
 * The failure a call result carries, or undefined when it succeeded.
 *
 * openapi-fetch leaves `error` unset for a non-2xx response with no
 * parsable body (a gateway 502, an empty 403), so `error` alone cannot
 * decide success. A failure always comes back defined here, with the
 * response status attached when the body did not carry one.
 */
export function resultError(result: SdkResult<unknown>): unknown {
  if (!requestFailed(result)) return undefined;
  const status = result.response?.status;
  const error = result.error;
  if (error === undefined || error === null) return { status };
  if (
    typeof error === 'object' &&
    status !== undefined &&
    typeof (error as { status?: unknown }).status !== 'number'
  ) {
    return { ...error, status };
  }
  return error;
}

export function apiErrorMessage(error: unknown, fallback: string, httpStatus?: number): string {
  const apiError = toApiError(error, fallback, httpStatus);
  const prefix = apiError.code ? `[${apiError.code}] ` : '';
  const fields = (apiError.details ?? [])
    .filter((detail) => detail.message)
    .map((detail) => `\n  ${detail.location ? `${detail.location}: ` : ''}${detail.message}`)
    .join('');
  const suffix = apiError.userAction ? `\n${apiError.userAction}` : '';
  return `${prefix}${apiError.message}${fields}${suffix}`;
}

/**
 * The exit code for a failed call. A 401 that reaches a command means the
 * refresh failed or was unavailable, which is an authentication failure.
 */
export function apiErrorExitCode(error: unknown): ExitCode {
  return toApiError(error, '').httpStatus === 401 ? EXIT_AUTH : EXIT_RUNTIME;
}
