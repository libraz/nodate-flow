import { toApiError } from '@nodate-flow/sdk/api-error';

export function apiErrorMessage(error: unknown, fallback: string, httpStatus?: number): string {
  const apiError = toApiError(error, fallback, httpStatus);
  const prefix = apiError.code ? `[${apiError.code}] ` : '';
  const suffix = apiError.userAction ? `\n${apiError.userAction}` : '';
  return `${prefix}${apiError.message}${suffix}`;
}
