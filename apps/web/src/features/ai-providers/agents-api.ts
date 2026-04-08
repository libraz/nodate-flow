/**
 * Agents feature — kill switch mutation (4.AGENT-3).
 *
 * Calls POST /workspaces/{wsId}/ai/agents/{agentId}/pause through the
 * typed SDK so 401/403 envelopes round-trip through the shared error
 * surface instead of the raw fetch escape hatch.
 */

import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export interface PauseAgentArgs {
  workspaceId: string;
  agentId: string;
  paused: boolean;
}

/** usePauseAgent toggles the `paused` column on an ai_agents row. */
export function usePauseAgent(): UseMutationResult<{ ok: true }, Error, PauseAgentArgs> {
  const qc = useQueryClient();
  return useMutation<{ ok: true }, Error, PauseAgentArgs>({
    mutationFn: async ({ workspaceId, agentId, paused }) => {
      const { error } = await sdk.POST('/workspaces/{wsId}/ai/agents/{agentId}/pause', {
        params: { path: { wsId: workspaceId, agentId } },
        body: { paused },
      });
      if (error) throw new Error('Failed to toggle agent pause');
      return { ok: true };
    },
    onSuccess: (_res, { workspaceId }) => {
      qc.invalidateQueries({ queryKey: ['ai-agents', workspaceId] });
    },
  });
}
