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
  today: (workspaceId: string) => [...aiCostKeys.all, 'today', workspaceId] as const,
};

/**
 * GET /workspaces/{wsId}/ai/cost-today.
 *
 * Non-suspense: returns `data: undefined` on error so callers can render
 * nothing without surfacing an error boundary. Refetches every 60 seconds.
 */
export function useAiCostTodayQuery(workspaceId: string | undefined): UseQueryResult<AiCostToday> {
  return useQuery({
    queryKey: aiCostKeys.today(workspaceId ?? ''),
    enabled: Boolean(workspaceId),
    retry: false,
    refetchInterval: 60_000,
    refetchOnWindowFocus: false,
    queryFn: async (): Promise<AiCostToday> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/cost-today', {
        params: { path: { wsId: workspaceId as string } },
      });
      if (error || !data) throw new Error('ai cost unavailable');
      return data;
    },
  });
}
