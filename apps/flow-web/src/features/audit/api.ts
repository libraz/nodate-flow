/**
 * Audit log feature — suspense query for listing workspace audit entries.
 *
 * The backend endpoint GET /workspaces/{wsId}/audit-logs is not yet
 * exposed in the OpenAPI spec. This module is written against the
 * expected shape so the UI is ready the moment the handler ships.
 * Until then the query will 404 and the ErrorBoundary catches it.
 */

import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

/** Shape matching the sqlc ListRecentAuditRow + API camelCase mapping. */
export interface AuditLogEntry {
  publicId: string;
  actorUserPublicId: string | null;
  actorDisplayName: string | null;
  action: string;
  resourceType: string;
  resourcePublicId: string | null;
  ipAddress: string | null;
  userAgent: string | null;
  metadataJson: Record<string, unknown> | null;
  occurredAt: number;
}

export interface AuditLogListResponse {
  entries: AuditLogEntry[];
  total: number;
}

export interface AuditLogFilters {
  action?: string;
  resourceType?: string;
  actorSearch?: string;
  dateFrom?: string;
  dateTo?: string;
  limit?: number;
  offset?: number;
}

/** Query key factory for the audit log feature. */
export const auditLogKeys = {
  all: ['audit-logs'] as const,
  list: (workspaceId: string, filters: AuditLogFilters) =>
    [...auditLogKeys.all, 'list', workspaceId, filters] as const,
};

/**
 * useAuditLogsQuery — suspense list of recent audit entries for the
 * given workspace. Backed by GET /workspaces/{wsId}/audit-logs.
 */
export function useAuditLogsQuery(
  workspaceId: string,
  filters: AuditLogFilters = {},
): UseSuspenseQueryResult<AuditLogListResponse> {
  return useSuspenseQuery({
    queryKey: auditLogKeys.list(workspaceId, filters),
    queryFn: async (): Promise<AuditLogListResponse> => {
      // Until the endpoint is added to the OpenAPI spec we use an
      // untyped GET via the SDK client.  Once the spec includes
      // /workspaces/{wsId}/audit-logs this can switch to the typed
      // overload.
      const queryParams: Record<string, unknown> = {
        limit: filters.limit ?? 50,
        offset: filters.offset ?? 0,
      };
      if (filters.action !== undefined) queryParams.action = filters.action;
      if (filters.resourceType !== undefined) queryParams.resourceType = filters.resourceType;
      if (filters.actorSearch !== undefined) queryParams.actorSearch = filters.actorSearch;
      if (filters.dateFrom !== undefined) queryParams.dateFrom = filters.dateFrom;
      if (filters.dateTo !== undefined) queryParams.dateTo = filters.dateTo;

      // TODO(openapi): Remove type suppression once GET /workspaces/{wsId}/audit-logs
      // is registered as a Huma operation and included in the merged OpenAPI spec.
      // Tracked by the audit-logs endpoint not yet being defined in flow-api handlers.
      const untypedSdk = sdk as unknown as {
        // biome-ignore lint/style/useNamingConvention: SDK method name
        GET: (
          url: string,
          opts: { params: { query: Record<string, unknown> } },
        ) => Promise<{ data?: AuditLogListResponse; error?: unknown }>;
      };
      const { data, error } = await untypedSdk.GET(`/workspaces/${workspaceId}/audit-logs`, {
        params: { query: queryParams },
      });
      if (error || !data) throw new Error('Failed to load audit logs');
      return { entries: data.entries ?? [], total: data.total ?? 0 };
    },
  });
}
