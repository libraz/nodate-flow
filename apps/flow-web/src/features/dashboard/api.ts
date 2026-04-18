/**
 * Dashboard feature — query key factory, types, and hooks for
 * dashboard widget CRUD and position updates.
 *
 * Types are defined inline because the SDK may not yet include these
 * endpoints. API calls use raw fetch via the shared base URL and auth
 * store token (same pattern as timeboxes).
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Widget type discriminator for dashboard widgets. */
export type WidgetType =
  | 'task_summary'
  | 'burndown'
  | 'signals_feed'
  | 'ai_suggestions'
  | 'overdue_tasks'
  | 'notification_feed';

/** Widget item returned by the list / detail API. Timestamps are unix seconds. */
export interface WidgetItem {
  id: string;
  widgetType: WidgetType;
  title: string;
  config: Record<string, unknown>;
  positionX: number;
  positionY: number;
  width: number;
  height: number;
  creatorDisplayName: string;
  updatedAt: number;
  createdAt: number;
  total: number;
}

/** Body for POST /workspaces/{wsId}/dashboard/widgets. */
export interface CreateWidgetInput {
  widgetType: WidgetType;
  title: string;
  config?: Record<string, unknown> | undefined;
  positionX?: number | undefined;
  positionY?: number | undefined;
  width?: number | undefined;
  height?: number | undefined;
}

/** Body for PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId}. */
export interface UpdateWidgetInput {
  title?: string | undefined;
  config?: Record<string, unknown> | undefined;
  positionX?: number | undefined;
  positionY?: number | undefined;
  width?: number | undefined;
  height?: number | undefined;
}

// ---------------------------------------------------------------------------
// Query key factory
// ---------------------------------------------------------------------------

/** Query key factory for the dashboard feature. */
export const dashboardKeys = {
  all: ['dashboard'] as const,
  list: (wsId: string) => [...dashboardKeys.all, 'list', wsId] as const,
  detail: (id: string) => [...dashboardKeys.all, 'detail', id] as const,
};

// ---------------------------------------------------------------------------
// Error helper
// ---------------------------------------------------------------------------

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as DashboardApiError };

// ---------------------------------------------------------------------------
// Fetch helpers
// ---------------------------------------------------------------------------

function authHeaders(): HeadersInit {
  const token = authStore.getState().accessToken;
  // biome-ignore lint/style/useNamingConvention: HTTP header name
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function fetchJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    throw toApiError(body, `Request failed with status ${String(res.status)}`);
  }
  return (await res.json()) as T;
}

async function fetchVoid(url: string, init?: RequestInit): Promise<void> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    throw toApiError(body, `Request failed with status ${String(res.status)}`);
  }
}

// ---------------------------------------------------------------------------
// Query hooks
// ---------------------------------------------------------------------------

/** GET /workspaces/{wsId}/dashboard/widgets — suspense query for the widget list. */
export function useWidgetsQuery(wsId: string): UseSuspenseQueryResult<WidgetItem[]> {
  return useSuspenseQuery({
    queryKey: dashboardKeys.list(wsId),
    queryFn: async (): Promise<WidgetItem[]> => {
      const data = await fetchJson<{ items?: WidgetItem[] }>(
        `${apiBaseUrl}/workspaces/${wsId}/dashboard/widgets?limit=200`,
      );
      return data.items ?? [];
    },
  });
}

// ---------------------------------------------------------------------------
// Mutation hooks
// ---------------------------------------------------------------------------

export interface CreateWidgetArgs {
  input: CreateWidgetInput;
}

/** POST /workspaces/{wsId}/dashboard/widgets — create a new widget. */
export function useCreateWidget(
  wsId: string,
): UseMutationResult<WidgetItem, ApiError, CreateWidgetArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ input }: CreateWidgetArgs): Promise<WidgetItem> => {
      return fetchJson<WidgetItem>(`${apiBaseUrl}/workspaces/${wsId}/dashboard/widgets`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: dashboardKeys.list(wsId) });
    },
  });
}

export interface UpdateWidgetArgs {
  widgetId: string;
  patch: UpdateWidgetInput;
}

/** PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId} — update a widget. */
export function useUpdateWidget(
  wsId: string,
): UseMutationResult<WidgetItem, ApiError, UpdateWidgetArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ widgetId, patch }: UpdateWidgetArgs): Promise<WidgetItem> => {
      return fetchJson<WidgetItem>(
        `${apiBaseUrl}/workspaces/${wsId}/dashboard/widgets/${widgetId}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(patch),
        },
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: dashboardKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: dashboardKeys.detail(vars.widgetId) });
    },
  });
}

export interface UpdateWidgetPositionArgs {
  widgetId: string;
  positionX: number;
  positionY: number;
}

/** PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId} — update position only. */
export function useUpdateWidgetPosition(
  wsId: string,
): UseMutationResult<WidgetItem, ApiError, UpdateWidgetPositionArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      widgetId,
      positionX,
      positionY,
    }: UpdateWidgetPositionArgs): Promise<WidgetItem> => {
      return fetchJson<WidgetItem>(
        `${apiBaseUrl}/workspaces/${wsId}/dashboard/widgets/${widgetId}`,
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ positionX, positionY }),
        },
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: dashboardKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: dashboardKeys.detail(vars.widgetId) });
    },
  });
}

/** DELETE /workspaces/{wsId}/dashboard/widgets/{widgetId} — remove a widget. */
export function useDeleteWidget(wsId: string): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (widgetId: string): Promise<void> => {
      await fetchVoid(`${apiBaseUrl}/workspaces/${wsId}/dashboard/widgets/${widgetId}`, {
        method: 'DELETE',
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: dashboardKeys.list(wsId) });
    },
  });
}
