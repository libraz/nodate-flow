import { QueryClient } from '@tanstack/react-query';

interface MaybeCodedError {
  code?: string;
}

function hasCode(err: unknown): err is MaybeCodedError {
  return typeof err === 'object' && err !== null && 'code' in err;
}

/**
 * Create the singleton QueryClient with project defaults.
 * Shared across all frontend apps for consistent caching behaviour.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: false,
        throwOnError: true,
        retry: (failureCount, error) => {
          if (hasCode(error) && error.code === 'AUTH.TOKEN.EXPIRED') return false;
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
