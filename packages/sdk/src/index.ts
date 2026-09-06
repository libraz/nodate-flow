// Public entry point for the generated nodate-flow SDK.
//
// The generated OpenAPI type module (./openapi) is produced by
// `make gen-sdk` which runs `openapi-typescript` against
// packages/sdk/openapi.json. The error-code modules under ./errors are
// produced by the errors codegen (scripts/gen-errors).

// Shared API error utilities
export {
  ApiError,
  NetworkError,
  type ProblemJson,
  toApiError,
  toNetworkError,
} from './api-error.js';
// Shared auth store (Zustand)
export {
  type AuthState,
  type AuthUser,
  authStore,
  selectAccessToken,
  selectIsAuthenticated,
  selectUser,
  useAuth,
} from './auth-store.js';
// Avatar URL helpers (auth-api proxy)
export { buildAvatarUrl } from './avatar.js';
export { type CreateClientOptions, createClient, type NodateFlowClient } from './client.js';
// Hand-authored lookup over the generated catalog; kept outside ./errors so
// the codegen barrel never clobbers it.
export {
  type ErrorDefinition,
  lookupErrorDefinition,
  lookupErrorI18nKey,
} from './error-lookup.js';
export * from './errors/index.js';
// Shared i18n provider
export { I18nProvider, type I18nProviderProps } from './i18n-provider.js';
export type { components, operations, paths } from './openapi.js';

// Shared TanStack Query client + provider
export {
  createQueryClient,
  queryClient,
  resetAuthErrorHandler,
  setAuthErrorHandler,
} from './query-client.js';
export { QueryProvider } from './query-provider.js';
// Redirect safety utilities
export { isSafeRedirect, parseAllowedOrigins } from './redirect.js';
export {
  type AuthRequestMiddlewareOptions,
  createAuthRequestMiddleware,
  createRefreshMiddleware,
  createTokenRefresher,
  decodeTokenExp,
  type RefreshMiddlewareOptions,
  type TokenRefresher,
} from './refresh.js';
// Region helpers (timezone + country)
export {
  detectTimezone,
  formatTimezoneLabel,
  groupTimezonesByRegion,
  listSupportedTimezones,
  SUPPORTED_COUNTRIES,
} from './region.js';
// The single door between feature code and the HTTP client
export {
  type ApiFailurePolicy,
  type ApiOutcome,
  type ApiRequester,
  type ApiRequestResult,
  createApiRequester,
  requestFailed,
  type SdkCall,
  type SdkResult,
} from './request.js';
export {
  lookup as lookupSignalKind,
  SIGNAL_KINDS,
  type SignalAutonomy,
  type SignalKind,
  type SignalKindDefinition,
  type SignalRetention,
  type SignalSubjectType,
} from './signal-kinds/index.js';
