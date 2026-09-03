/**
 * AI invocations feature — suspense query for the workspace audit
 * panel that lists recent redacted LLM calls.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';

export type AiInvocation = components['schemas']['Invocation'];

export const aiInvocationsKeys = {
  all: ['ai-invocations'] as const,
  list: (workspaceId: string) => [...aiInvocationsKeys.all, 'list', workspaceId] as const,
};

/**
 * useAiInvocationsQuery — suspense list of recent redacted LLM calls
 * for the given workspace. Backed by GET /workspaces/{wsId}/ai/invocations.
 */
export function useAiInvocationsQuery(workspaceId: string): UseSuspenseQueryResult<AiInvocation[]> {
  return useSuspenseQuery({
    queryKey: aiInvocationsKeys.list(workspaceId),
    queryFn: async (): Promise<AiInvocation[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/invocations', {
            params: { path: { wsId: workspaceId }, query: { limit: 50, offset: 0 } },
          }),
        'Failed to load AI invocations',
      );
      return data.invocations ?? [];
    },
  });
}
