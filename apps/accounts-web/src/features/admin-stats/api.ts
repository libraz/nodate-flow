/**
 * Admin instance stats — typed query against `GET /admin/instance-stats`.
 *
 * The accounts-web SDK is untyped at the path level (auth-api endpoints
 * are not in the shared OpenAPI spec), so we narrow the response shape
 * locally with a runtime check + cast.
 */

import { type UseQueryResult, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';

/** Fields returned from `GET /admin/instance-stats`. */
export interface InstanceStats {
  totalUsers: number;
  totalWorkspaces: number;
}

/** Query key factory for the admin-stats feature. */
export const adminStatsKeys = {
  all: ['admin', 'instance-stats'] as const,
};

function isInstanceStats(value: unknown): value is InstanceStats {
  if (typeof value !== 'object' || value === null) return false;
  const v = value as Record<string, unknown>;
  return typeof v.totalUsers === 'number' && typeof v.totalWorkspaces === 'number';
}

/**
 * Fetches instance-wide health counters. Uses `useQuery` (not Suspense)
 * so the page can render its scaffold + manage its own error / refetch
 * UI inline, matching the controlled-feel of an admin dashboard.
 */
export function useInstanceStatsQuery(): UseQueryResult<InstanceStats, ApiError> {
  return useQuery<InstanceStats, ApiError>({
    queryKey: adminStatsKeys.all,
    // Surface the error inline immediately. The page renders a Retry
    // button so a transient failure can be re-attempted explicitly;
    // background retries would hide the failure for several seconds.
    retry: false,
    // The shared QueryClient defaults to `throwOnError: true` so queries
    // escalate to the route-level error boundary. Stats has its own
    // inline `role="alert"` block — keep the failure local to this card.
    throwOnError: false,
    queryFn: async (): Promise<InstanceStats> => {
      const data = await apiRequest(
        (client) => client.GET('/admin/instance-stats'),
        'Failed to load instance stats',
      );
      if (!isInstanceStats(data)) {
        throw new ApiError(undefined, 'Unexpected instance stats payload', 500);
      }
      return { totalUsers: data.totalUsers, totalWorkspaces: data.totalWorkspaces };
    },
  });
}

/** Hook returning a stable invalidator for the instance-stats query. */
export function useInvalidateInstanceStats(): () => Promise<void> {
  const qc = useQueryClient();
  return async (): Promise<void> => {
    await qc.invalidateQueries({ queryKey: adminStatsKeys.all });
  };
}
