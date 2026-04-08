/**
 * Agents feature — kill switch mutation (4.AGENT-3).
 *
 * Calls POST /workspaces/{wsId}/ai/agents/{agentId}/pause through the
 * typed SDK so 401/403 envelopes round-trip through the shared error
 * surface instead of the raw fetch escape hatch.
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

/**
 * The four trigger modes mirror the ai_agents.schedule_kind ENUM
 * defined in sql/tables/ai_agents.sql. Keep this list in sync with
 * the backend enum and the OpenAPI UpdateAgentSchedule body schema.
 */
export const AGENT_SCHEDULE_KINDS = ['disabled', 'interval', 'on_event', 'manual'] as const;
export type AgentScheduleKind = (typeof AGENT_SCHEDULE_KINDS)[number];

export interface AgentSummary {
  id: string;
  name: string;
  description?: string;
  systemPrompt: string;
  modelId: string;
  modelName: string;
  scheduleKind: AgentScheduleKind;
  paused: boolean;
  createdAt: number;
  updatedAt?: number;
}

/** useAgentsQuery lists the workspace's AI agents. */
export function useAgentsQuery(
  workspaceId: string,
): UseSuspenseQueryResult<{ total: number; agents: AgentSummary[] }> {
  return useSuspenseQuery({
    queryKey: ['ai-agents', workspaceId],
    queryFn: async () => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/agents', {
        params: { path: { wsId: workspaceId }, query: { limit: 200, offset: 0 } },
      });
      if (error || !data) throw new Error('Failed to list agents');
      return {
        total: data.total ?? 0,
        agents: (data.agents ?? []) as AgentSummary[],
      };
    },
  });
}

export interface UpdateAgentScheduleArgs {
  workspaceId: string;
  agentId: string;
  scheduleKind: AgentScheduleKind;
}

/**
 * useUpdateAgentSchedule PATCHes the schedule_kind on an ai_agents
 * row. The scheduler picks up the change on its next tick (no server
 * restart required).
 */
export function useUpdateAgentSchedule(): UseMutationResult<
  { ok: true },
  Error,
  UpdateAgentScheduleArgs
> {
  const qc = useQueryClient();
  return useMutation<{ ok: true }, Error, UpdateAgentScheduleArgs>({
    mutationFn: async ({ workspaceId, agentId, scheduleKind }) => {
      const { error } = await sdk.PATCH('/workspaces/{wsId}/ai/agents/{agentId}/schedule', {
        params: { path: { wsId: workspaceId, agentId } },
        body: { scheduleKind },
      });
      if (error) throw new Error('Failed to update agent schedule');
      return { ok: true };
    },
    onSuccess: (_res, { workspaceId }) => {
      qc.invalidateQueries({ queryKey: ['ai-agents', workspaceId] });
    },
  });
}

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
