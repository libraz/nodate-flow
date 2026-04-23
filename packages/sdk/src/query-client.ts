import { QueryCache, QueryClient } from '@tanstack/react-query';

interface MaybeCodedError {
  code?: string;
}

interface MaybeStatusError {
  /** HTTP status on {@link ApiError} and related error types. */
  httpStatus?: number;
  /**
   * Legacy field used by feature-specific subclasses that were created
   * before {@link ApiError} carried an HTTP status of its own (e.g.
   * `AiProvidersQueryError`). Treated as an alias of `httpStatus`.
   */
  status?: number;
}

function hasCode(err: unknown): err is MaybeCodedError {
  return typeof err === 'object' && err !== null && 'code' in err;
}

function httpStatusOf(err: unknown): number | undefined {
  if (typeof err !== 'object' || err === null) return undefined;
  const e = err as MaybeStatusError;
  if (typeof e.httpStatus === 'number') return e.httpStatus;
  if (typeof e.status === 'number') return e.status;
  return undefined;
}

/**
 * App-provided handler invoked once when a query error surfaces with
 * an HTTP 401 status, meaning the access token is terminally rejected
 * (the refresh middleware already tried and failed, or the request
 * bypasses the SDK client entirely).
 *
 * Apps register their own cleanup (clear session, clear persisted
 * workspace, navigate to /login) via {@link setAuthErrorHandler}.
 */
let onAuthError: (() => void) | null = null;
let authErrorFired = false;

/**
 * Register a global 401 handler for the shared QueryClient. The handler
 * fires at most once until {@link resetAuthErrorHandler} is called, so
 * a 401 storm from a polling query collapses to a single redirect.
 *
 * Returns an unregister function for tests.
 */
export function setAuthErrorHandler(handler: (() => void) | null): () => void {
  onAuthError = handler;
  authErrorFired = false;
  return () => {
    if (onAuthError === handler) {
      onAuthError = null;
      authErrorFired = false;
    }
  };
}

/**
 * Reset the fired-once latch on the auth-error handler. Useful in tests
 * and after a successful login so a subsequent session can also detect
 * a terminal 401.
 */
export function resetAuthErrorHandler(): void {
  authErrorFired = false;
}

function handleQueryError(error: unknown): void {
  if (!onAuthError || authErrorFired) return;
  if (httpStatusOf(error) !== 401) return;
  authErrorFired = true;
  try {
    onAuthError();
  } catch {
    /* handler must not throw back into the cache */
  }
}

/**
 * Create the singleton QueryClient with project defaults.
 * Shared across all frontend apps for consistent caching behaviour.
 *
 * Any query that errors with HTTP 401 triggers the global auth-error
 * handler registered via {@link setAuthErrorHandler}, which fires at
 * most once per session so the user is redirected to /login even if a
 * background poll (e.g. notifications unread-count) keeps firing.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    queryCache: new QueryCache({
      onError: (error) => {
        handleQueryError(error);
      },
    }),
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: false,
        throwOnError: true,
        retry: (failureCount, error) => {
          if (hasCode(error) && error.code === 'AUTH.TOKEN.EXPIRED') return false;
          if (httpStatusOf(error) === 401) return false;
          return failureCount < 1;
        },
      },
      mutations: {
        throwOnError: false,
      },
    },
  });
}

/** Module-level singleton QueryClient instance. */
export const queryClient = createQueryClient();
