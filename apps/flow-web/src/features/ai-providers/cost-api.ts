/**
 * AI cost-today feature — non-suspense query for the topbar cost meter.
 *
 * Degrades silently when AI is not configured: errors are swallowed by
 * TanStack Query and the caller renders nothing on `data === undefined`.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseQueryResult, useQuery } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type AiCostToday = components['schemas']['AiCostTodayOutputBody'];

/** Query key factory for AI cost endpoints. */
export const aiCostKeys = {
  all: ['ai-cost'] as const,
  today: (workspaceId: string, tz: string) =>
    [...aiCostKeys.all, 'today', workspaceId, tz] as const,
};

/**
 * GET /workspaces/{wsId}/ai/cost-today.
 *
 * Non-suspense: returns `data: undefined` on error so callers can render
 * nothing without surfacing an error boundary. Refetches every 60 seconds.
 *
 * The browser's resolved IANA timezone is forwarded to the backend so the
 * day window (and returned `date`) matches the viewer's local calendar day
 * instead of UTC. The tz is included in the query key so the cache busts
 * when the browser tz changes (travel, VM clock fix, etc.).
 */
export function useAiCostTodayQuery(workspaceId: string | undefined): UseQueryResult<AiCostToday> {
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return useQuery({
    queryKey: aiCostKeys.today(workspaceId ?? '', tz),
    enabled: Boolean(workspaceId),
    retry: false,
    refetchInterval: 60_000,
    refetchOnWindowFocus: false,
    // This panel is decorative; opt out of the SDK-wide `throwOnError: true`
    // default so a 404 / AI-disabled response never cascades to the route
    // ErrorBoundary. Callers render nothing on error.
    throwOnError: false,
    queryFn: async (): Promise<AiCostToday> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/cost-today', {
        params: { path: { wsId: workspaceId as string }, query: { tz } },
      });
      if (error || !data) throw new Error('ai cost unavailable');
      return data;
    },
  });
}
