/**
 * Agent work on a task — proposals awaiting a person, run requests and the
 * per-run step log.
 *
 * Every key sits under `tasksKeys.detail(taskId)`, so the `task.changed`
 * stream event (which invalidates `tasksKeys.all`) refreshes proposals and
 * run history without a separate registry entry.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiRequest } from '../../../lib/api';
import type { ApiError } from '../../../lib/api-error';
import { timelineKeys } from '../../timeline/api';
import { fetchWorkspace, workspacesKeys } from '../../workspaces/api';
import { tasksKeys } from '../api';

export type AgentProposal = components['schemas']['AgentProposal'];
export type AgentStep = components['schemas']['AgentStep'];
export type AgentProposalDecision = components['schemas']['AgentProposalDecision'];

export const agentWorkKeys = {
  proposals: (taskId: string) => [...tasksKeys.detail(taskId), 'agent-proposals'] as const,
  steps: (taskId: string, runKey: string) =>
    [...tasksKeys.agentRuns(taskId), 'steps', runKey] as const,
};

/** GET /tasks/{id}/agent-proposals?status=pending — every live pending proposal. */
export function useAgentProposalsQuery(taskId: string): UseSuspenseQueryResult<AgentProposal[]> {
  return useSuspenseQuery({
    queryKey: agentWorkKeys.proposals(taskId),
    queryFn: async (): Promise<AgentProposal[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks/{id}/agent-proposals', {
            params: { path: { id: taskId }, query: { status: 'pending' } },
          }),
        'Failed to load agent proposals',
      );
      return data.proposals ?? [];
    },
  });
}

/** GET /tasks/{id}/agent-runs/{runKey}/steps — loaded only once a run is opened. */
export function useAgentRunStepsQuery(
  taskId: string,
  runKey: string,
): UseSuspenseQueryResult<AgentStep[]> {
  return useSuspenseQuery({
    queryKey: agentWorkKeys.steps(taskId, runKey),
    queryFn: async (): Promise<AgentStep[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks/{id}/agent-runs/{runKey}/steps', {
            params: { path: { id: taskId, runKey } },
          }),
        'Failed to load agent run steps',
      );
      return data.steps ?? [];
    },
  });
}

type Invalidator = ReturnType<typeof useQueryClient>;

/** Proposals, task detail, comments, run history and the agent list, plus the task timeline. */
function invalidateAgentWork(qc: Invalidator, taskId: string): void {
  void qc.invalidateQueries({ queryKey: agentWorkKeys.proposals(taskId) });
  void qc.invalidateQueries({ queryKey: tasksKeys.detail(taskId) });
  void qc.invalidateQueries({ queryKey: tasksKeys.comments(taskId) });
  void qc.invalidateQueries({ queryKey: tasksKeys.agentRuns(taskId) });
  void qc.invalidateQueries({ queryKey: tasksKeys.agentActors(taskId) });
  void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', taskId] });
}

export interface DecideProposalArgs {
  taskId: string;
  proposalId: string;
}

/**
 * POST .../approve. Settles with an invalidation on failure too: a stale,
 * expired or already-decided refusal means the list on screen is wrong.
 */
export function useApproveAgentProposal(): UseMutationResult<
  AgentProposalDecision,
  ApiError,
  DecideProposalArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, proposalId }: DecideProposalArgs) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/agent-proposals/{proposalId}/approve', {
            params: { path: { id: taskId, proposalId } },
          }),
        'Failed to approve agent proposal',
      ),
    onSettled: (_data, _err, vars) => {
      invalidateAgentWork(qc, vars.taskId);
    },
  });
}

export interface RejectProposalArgs extends DecideProposalArgs {
  reason?: string;
}

/** POST .../reject; the reason is read by the agent on its next run. */
export function useRejectAgentProposal(): UseMutationResult<
  AgentProposalDecision,
  ApiError,
  RejectProposalArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, proposalId, reason }: RejectProposalArgs) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/agent-proposals/{proposalId}/reject', {
            params: { path: { id: taskId, proposalId } },
            body: reason ? { reason } : {},
          }),
        'Failed to reject agent proposal',
      ),
    onSettled: (_data, _err, vars) => {
      invalidateAgentWork(qc, vars.taskId);
    },
  });
}

export interface RunAgentNowArgs {
  taskId: string;
  agentId: string;
}

/** POST /tasks/{id}/agents/{agentId}/run — 202; the runtime picks the request up. */
export function useRunAgentNow(): UseMutationResult<
  components['schemas']['RunAgentBody'],
  ApiError,
  RunAgentNowArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, agentId }: RunAgentNowArgs) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/agents/{agentId}/run', {
            params: { path: { id: taskId, agentId } },
          }),
        'Failed to request an agent run',
      ),
    onSuccess: (_data, vars) => {
      invalidateAgentWork(qc, vars.taskId);
    },
  });
}

/**
 * Whether the viewer may ask agents to work and decide their proposals.
 * Guests cannot; an unknown role (still loading, lookup failed) is allowed
 * through because the server refuses what it must.
 */
export function useCanActOnAgentWork(workspaceId: string): boolean {
  const { data } = useQuery({
    queryKey: workspacesKeys.detail(workspaceId),
    queryFn: () => fetchWorkspace(workspaceId),
    throwOnError: false,
  });
  return data?.role !== 'guest';
}
