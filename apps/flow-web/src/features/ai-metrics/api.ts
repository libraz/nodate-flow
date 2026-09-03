/**
 * AI metrics feature — suspense query for the workspace AI metrics
 * dashboard.
 *
 * Backed by `GET /workspaces/{wsId}/ai/metrics?windowDays=N`. Surfaces
 * proposal / acceptance counters and per-provider egress rate-limit
 * stats over a configurable rolling window (1-365 days; the dashboard
 * exposes 7 / 30 / 90 day presets).
 */

import type { components } from '@nodate-flow/sdk';
import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';

/** Per-provider outbound rate-limit row from the metrics response. */
export type OutboundLimitStat = components['schemas']['OutboundLimitStat'];

/** Full AI metrics payload for one workspace + window. */
export type AiMetrics = components['schemas']['MetricsOutputBody'];

/** Allowed dashboard window presets. */
export type AiMetricsWindow = 7 | 30 | 90;

/** Default window when the URL search param is missing or invalid. */
export const DEFAULT_WINDOW: AiMetricsWindow = 30;

/** Ordered list of supported window presets (used by the segmented control). */
export const SUPPORTED_WINDOWS: readonly AiMetricsWindow[] = [7, 30, 90] as const;

/**
 * Coerce an unknown URL search value into a {@link AiMetricsWindow},
 * falling back to {@link DEFAULT_WINDOW} when the value is missing or
 * not one of the supported presets.
 */
export function coerceWindowDays(raw: unknown): AiMetricsWindow {
  const n = typeof raw === 'string' ? Number(raw) : typeof raw === 'number' ? raw : Number.NaN;
  if (n === 7 || n === 30 || n === 90) return n;
  return DEFAULT_WINDOW;
}

/** Query key factory for AI metrics endpoints. */
export const aiMetricsKeys = {
  all: ['ai-metrics'] as const,
  workspace: (workspaceId: string, windowDays: AiMetricsWindow) =>
    [...aiMetricsKeys.all, workspaceId, windowDays] as const,
};

/**
 * useAiMetricsQuery — suspense query for one workspace's AI metrics.
 *
 * The query suspends on first load and on window-day changes; callers
 * should wrap the consuming component in a `<Suspense>` boundary that
 * renders skeletons.
 *
 * @param workspaceId - Workspace public id.
 * @param windowDays - Rolling window in days. Must be one of
 *                     {@link SUPPORTED_WINDOWS}.
 */
export function useAiMetricsQuery(
  workspaceId: string,
  windowDays: AiMetricsWindow,
): UseSuspenseQueryResult<AiMetrics> {
  return useSuspenseQuery({
    queryKey: aiMetricsKeys.workspace(workspaceId, windowDays),
    queryFn: async (): Promise<AiMetrics> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/metrics', {
            params: { path: { wsId: workspaceId }, query: { windowDays } },
          }),
        'Failed to load AI metrics',
      );
      return data;
    },
  });
}
