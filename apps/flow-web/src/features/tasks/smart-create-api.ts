/**
 * Smart-create API hooks — AI-assisted task decomposition and assignee
 * suggestion powered by the propose-smart / apply-smart endpoints.
 */

import { useMutation, useQueryClient } from '@tanstack/react-query';

import { ApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';
import { tasksKeys } from './api';

export { ApiError as TaskApiError };

export interface AssigneeSuggestion {
  userPublicId: string;
  displayName: string;
  confidence: number;
  reason: string;
}

export interface SubtaskProposal {
  title: string;
  description: string;
  priority: string;
  assignee?: AssigneeSuggestion;
}

export interface SmartProposal {
  suggestedAssignees: AssigneeSuggestion[];
  subtasks: SubtaskProposal[];
}

export interface ProposeSmartArgs {
  workspaceId: string;
  projectId: string;
  title: string;
  description: string;
}

/**
 * useProposeSmartTask — POST /workspaces/{wsId}/tasks/propose-smart.
 *
 * Calls the AI-backed endpoint to get assignee suggestions and subtask
 * decomposition based on past ticket patterns.
 */
export function useProposeSmartTask() {
  return useMutation<SmartProposal, ApiError, ProposeSmartArgs>({
    mutationFn: async (args) => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/tasks/propose-smart', {
        params: { path: { wsId: args.workspaceId } },
        body: {
          projectId: args.projectId,
          title: args.title,
          description: args.description,
        },
      });
      if (error || !data) {
        const err = error as { detail?: string; title?: string; type?: string } | undefined;
        throw new ApiError(
          err?.type,
          err?.detail ?? err?.title ?? 'Failed to get smart suggestions',
        );
      }
      return data as SmartProposal;
    },
  });
}

export interface ApplySmartSubtask {
  title: string;
  description: string;
  priority: number;
  assigneeUserId?: string;
}

export interface ApplySmartArgs {
  workspaceId: string;
  projectId: string;
  title: string;
  description: string;
  priority: number;
  assigneeUserIds: string[];
  subtasks: ApplySmartSubtask[];
}

export interface ApplySmartResult {
  taskId: string;
  subtaskIds: string[];
}

/**
 * useApplySmartTask — POST /workspaces/{wsId}/tasks/apply-smart.
 *
 * Creates the parent task with selected assignees and subtasks in a
 * single atomic call.
 */
export function useApplySmartTask() {
  const qc = useQueryClient();
  return useMutation<ApplySmartResult, ApiError, ApplySmartArgs>({
    mutationFn: async (args) => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/tasks/apply-smart', {
        params: { path: { wsId: args.workspaceId } },
        body: {
          projectId: args.projectId,
          title: args.title,
          description: args.description,
          priority: args.priority,
          assigneeUserIds: args.assigneeUserIds,
          subtasks: args.subtasks,
        },
      });
      if (error || !data) {
        const err = error as { detail?: string; title?: string; type?: string } | undefined;
        throw new ApiError(err?.type, err?.detail ?? err?.title ?? 'Failed to apply smart create');
      }
      return data as ApplySmartResult;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list', vars.projectId] });
      // Subtasks may also appear in `me` lists when an assignee was supplied,
      // so broadcast the broader prefix as well.
      void qc.invalidateQueries({ queryKey: tasksKeys.myInfinite() });
    },
  });
}
