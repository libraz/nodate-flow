// Public entry point for the generated nodate-flow SDK.
//
// The generated OpenAPI type module (./openapi) is produced by
// `make gen-sdk` which runs `openapi-typescript` against
// packages/sdk/openapi.json. The error-code modules under ./errors are
// produced by the errors codegen (scripts/gen-errors).

export type { paths, components, operations } from './openapi.js';
export { createClient, type CreateClientOptions, type NodateFlowClient } from './client.js';
export * from './errors/index.js';
export {
  createTokenRefresher,
  createRefreshMiddleware,
  createAuthRequestMiddleware,
  decodeTokenExp,
  type RefreshMiddlewareOptions,
  type AuthRequestMiddlewareOptions,
  type TokenRefresher,
} from './refresh.js';

// Shared auth store (Zustand)
export {
  authStore,
  useAuth,
  selectAccessToken,
  selectUser,
  selectIsAuthenticated,
  type AuthUser,
  type AuthState,
} from './auth-store.js';

// Shared API error utilities
export { ApiError, toApiError, type ProblemJson } from './api-error.js';

// Shared TanStack Query client + provider
export {
  createQueryClient,
  queryClient,
  setAuthErrorHandler,
  resetAuthErrorHandler,
} from './query-client.js';
export { QueryProvider } from './query-provider.js';

// Shared i18n provider
export { I18nProvider, type I18nProviderProps } from './i18n-provider.js';

// Redirect safety utilities
export { isSafeRedirect } from './redirect.js';

// Region helpers (timezone + country)
export {
  SUPPORTED_COUNTRIES,
  listSupportedTimezones,
  detectTimezone,
  groupTimezonesByRegion,
  formatTimezoneLabel,
} from './region.js';
