/**
 * Re-export the shared API error utilities from @nodate-flow/sdk.
 * Kept as a bridge so existing imports continue to resolve.
 */
export { ApiError, toApiError, type ProblemJson } from '@nodate-flow/sdk';
