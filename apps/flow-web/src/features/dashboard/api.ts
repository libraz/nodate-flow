/**
 * Dashboard feature — query key factory, types, and hooks for
 * dashboard widget CRUD and position updates.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';

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

type SDKWidget = components['schemas']['WidgetDTO'];

/** Widget item returned by the list / detail API. Timestamps are unix seconds. */
export type WidgetItem = Omit<SDKWidget, 'config' | 'widgetType'> & {
  widgetType: WidgetType;
  config?: Record<string, unknown>;
};

/** Body for POST /workspaces/{wsId}/dashboard/widgets. */
export type CreateWidgetInput = components['schemas']['CreateWidgetBody'];

/** Body for PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId}. */
export type UpdateWidgetInput = components['schemas']['UpdateWidgetBody'];

// ---------------------------------------------------------------------------
// Query key factory
// ---------------------------------------------------------------------------

/** Query key factory for the dashboard feature. */
export const dashboardKeys = {
  all: ['dashboard'] as const,
  list: (wsId: string) => [...dashboardKeys.all, 'list', wsId] as const,
  detail: (id: string) => [...dashboardKeys.all, 'detail', id] as const,
};

export { ApiError as DashboardApiError };

function toWidgetItem(widget: SDKWidget): WidgetItem {
  const { config, widgetType, ...rest } = widget;
  const out: WidgetItem = {
    ...rest,
    widgetType: widgetType as WidgetType,
  };
  if (config && typeof config === 'object' && !Array.isArray(config)) {
    out.config = config as Record<string, unknown>;
  }
  return out;
}

// ---------------------------------------------------------------------------
// Query hooks
// ---------------------------------------------------------------------------

/** GET /workspaces/{wsId}/dashboard/widgets — suspense query for the widget list. */
export function useWidgetsQuery(wsId: string): UseSuspenseQueryResult<WidgetItem[]> {
  return useSuspenseQuery({
    queryKey: dashboardKeys.list(wsId),
    queryFn: async (): Promise<WidgetItem[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/dashboard/widgets', {
            params: { path: { wsId }, query: { limit: 200 } },
          }),
        'Failed to load dashboard widgets',
      );
      return (data.widgets ?? []).map(toWidgetItem);
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
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/dashboard/widgets', {
            params: { path: { wsId } },
            body: input,
          }),
        'Failed to create dashboard widget',
      );
      return toWidgetItem(data);
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
      const data = await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/dashboard/widgets/{widgetId}', {
            params: { path: { wsId, widgetId } },
            body: patch,
          }),
        'Failed to update dashboard widget',
      );
      return toWidgetItem(data);
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
  width: number;
  height: number;
  sortWeight: number;
}

/** PUT /workspaces/{wsId}/dashboard/widgets/{widgetId}/position — update position only. */
export function useUpdateWidgetPosition(
  wsId: string,
): UseMutationResult<WidgetItem, ApiError, UpdateWidgetPositionArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      widgetId,
      positionX,
      positionY,
      width,
      height,
      sortWeight,
    }: UpdateWidgetPositionArgs): Promise<WidgetItem> => {
      const data = await apiRequest(
        (client) =>
          client.PUT('/workspaces/{wsId}/dashboard/widgets/{widgetId}/position', {
            params: { path: { wsId, widgetId } },
            body: { positionX, positionY, width, height, sortWeight },
          }),
        'Failed to update dashboard widget position',
      );
      return toWidgetItem(data);
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
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/dashboard/widgets/{widgetId}', {
            params: { path: { wsId, widgetId } },
          }),
        'Failed to delete dashboard widget',
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: dashboardKeys.list(wsId) });
    },
  });
}
