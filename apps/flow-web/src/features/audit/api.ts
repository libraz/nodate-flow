/**
 * Audit log feature — suspense query for listing workspace audit entries.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';

export type AuditLogEntry = components['schemas']['LogEntryDTO'];
export type AuditLogListResponse = {
  entries: AuditLogEntry[];
  total: number;
};

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
      const queryParams: AuditLogFilters = {
        limit: filters.limit ?? 50,
        offset: filters.offset ?? 0,
      };
      if (filters.action !== undefined) queryParams.action = filters.action;
      if (filters.resourceType !== undefined) queryParams.resourceType = filters.resourceType;
      if (filters.actorSearch !== undefined) queryParams.actorSearch = filters.actorSearch;
      if (filters.dateFrom !== undefined) queryParams.dateFrom = filters.dateFrom;
      if (filters.dateTo !== undefined) queryParams.dateTo = filters.dateTo;

      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/audit-logs', {
            params: { path: { wsId: workspaceId }, query: queryParams },
          }),
        'Failed to load audit logs',
      );
      return { entries: data.entries ?? [], total: data.total ?? 0 };
    },
  });
}
