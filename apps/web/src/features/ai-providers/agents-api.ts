/**
 * Agents feature — minimal hooks for the 4.AGENT-3 kill switch UI.
 *
 * NOTE: a full PATCH /workspaces/{wsId}/ai/agents/{agentId} REST
 * surface is not yet wired in the API; this hook scaffolds the
 * client-side interface so the dock can render a "pause" toggle as
 * soon as the backend lands.
 */

import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';

export interface PauseAgentArgs {
  workspaceId: string;
  agentId: string;
  paused: boolean;
}

/**
 * usePauseAgent — placeholder mutation. Calls a future
 * `/workspaces/{wsId}/ai/agents/{agentId}/pause` endpoint via
 * window.fetch so it ships ahead of the typed SDK update. Returns
 * `{ ok: true }` on 2xx.
 */
export function usePauseAgent(): UseMutationResult<{ ok: true }, Error, PauseAgentArgs> {
  const qc = useQueryClient();
  return useMutation<{ ok: true }, Error, PauseAgentArgs>({
    mutationFn: async ({ workspaceId, agentId, paused }) => {
      const res = await fetch(
        `/workspaces/${encodeURIComponent(workspaceId)}/ai/agents/${encodeURIComponent(
          agentId,
        )}/pause`,
        {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ paused }),
        },
      );
      if (!res.ok) throw new Error('Failed to toggle agent pause');
      return { ok: true };
    },
    onSuccess: (_res, { workspaceId }) => {
      qc.invalidateQueries({ queryKey: ['ai-agents', workspaceId] });
    },
  });
}
