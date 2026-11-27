import { QueryCache, QueryClient } from '@tanstack/react-query';

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

const RATE_LIMIT_MAX_RETRIES = 3;
const RETRY_MAX_DELAY_MS = 30_000;

function retryAfterOf(err: unknown): number | undefined {
  if (typeof err !== 'object' || err === null) return undefined;
  const v = (err as { retryAfterSeconds?: unknown }).retryAfterSeconds;
  return typeof v === 'number' ? v : undefined;
}

/** Retry policy: never 401, up to 3 on 429, one retry for everything else. */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  const status = httpStatusOf(error);
  if (status === 401) return false;
  if (status === 429) return failureCount < RATE_LIMIT_MAX_RETRIES;
  return failureCount < 1;
}

/** Honours `Retry-After` on a 429; otherwise exponential backoff. Capped at 30s. */
export function queryRetryDelay(failureCount: number, error: unknown): number {
  const retryAfter = httpStatusOf(error) === 429 ? retryAfterOf(error) : undefined;
  if (retryAfter !== undefined) return Math.min(retryAfter * 1000, RETRY_MAX_DELAY_MS);
  return Math.min(1000 * 2 ** failureCount, RETRY_MAX_DELAY_MS);
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
        // A failed background refetch keeps showing the data it already has.
        throwOnError: (_error, query) => query.state.data === undefined,
        retry: shouldRetryQuery,
        retryDelay: queryRetryDelay,
      },
      mutations: {
        throwOnError: false,
      },
    },
  });
}

/** Module-level singleton QueryClient instance. */
export const queryClient = createQueryClient();
