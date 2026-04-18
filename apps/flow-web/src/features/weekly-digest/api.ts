/**
 * Weekly digest feature — GET /workspaces/{wsId}/ai/weekly-digest.
 *
 * Suspense query powering the workspace weekly digest view (2.AI-9).
 * The backend rule engine produces counts, task lists, and a
 * pre-rendered markdown body; this hook is a thin fetch wrapper.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type WeeklyDigest = components['schemas']['WeeklyDigestOutputBody'];
export type WeeklyDigestTask = components['schemas']['WeeklyDigestTask'];
export type WeeklyDigestCounts = components['schemas']['WeeklyDigestCounts'];

export const weeklyDigestKeys = {
  all: ['weekly-digest'] as const,
  forWorkspace: (workspaceId: string) =>
    [...weeklyDigestKeys.all, 'workspace', workspaceId] as const,
};

/**
 * useWeeklyDigestQuery — suspense read of the deterministic weekly
 * digest for the given workspace.
 */
export function useWeeklyDigestQuery(workspaceId: string): UseSuspenseQueryResult<WeeklyDigest> {
  return useSuspenseQuery({
    queryKey: weeklyDigestKeys.forWorkspace(workspaceId),
    queryFn: async (): Promise<WeeklyDigest> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/weekly-digest', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw new Error('Failed to load weekly digest');
      return data;
    },
  });
}
