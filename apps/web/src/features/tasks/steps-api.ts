/**
 * Step decomposition API hooks — AI-assisted task decomposition into
 * subtasks via the propose-steps / apply-steps endpoints.
 */

import { useMutation, useQueryClient } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';
import { TaskApiError, tasksKeys } from './api';

export interface StepProposal {
  title: string;
  description: string;
  priority: string;
}

export interface ProposeStepsResult {
  parentTaskId: string;
  steps: StepProposal[];
}

/**
 * useProposeSteps — POST /tasks/{id}/propose-steps.
 *
 * Calls the AI-backed endpoint to decompose a task into subtask steps.
 */
export function useProposeSteps() {
  return useMutation<ProposeStepsResult, TaskApiError, string>({
    mutationFn: async (taskId) => {
      const { data, error } = await sdk.POST('/tasks/{id}/propose-steps', {
        params: { path: { id: taskId } },
      });
      if (error || !data) {
        const err = error as { detail?: string; title?: string; type?: string } | undefined;
        throw new TaskApiError(err?.type, err?.detail ?? err?.title ?? 'Failed to propose steps');
      }
      return data as ProposeStepsResult;
    },
  });
}

export interface ApplyStepsArgs {
  taskId: string;
  steps: { title: string; description: string; priority: number }[];
}

export interface ApplyStepsResult {
  created: string[];
}

/**
 * useApplySteps — POST /tasks/{id}/apply-steps.
 *
 * Creates subtasks from the selected proposed steps.
 */
export function useApplySteps() {
  const qc = useQueryClient();
  return useMutation<ApplyStepsResult, TaskApiError, ApplyStepsArgs>({
    mutationFn: async (args) => {
      const { data, error } = await sdk.POST('/tasks/{id}/apply-steps', {
        params: { path: { id: args.taskId } },
        body: { steps: args.steps },
      });
      if (error || !data) {
        const err = error as { detail?: string; title?: string; type?: string } | undefined;
        throw new TaskApiError(err?.type, err?.detail ?? err?.title ?? 'Failed to apply steps');
      }
      return data as ApplyStepsResult;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
    },
  });
}
