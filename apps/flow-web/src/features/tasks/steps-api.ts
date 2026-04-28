/**
 * Step decomposition API hooks — AI-assisted task decomposition into
 * subtasks via the propose-steps / apply-steps endpoints.
 */

import { useMutation, useQueryClient } from '@tanstack/react-query';

import { ApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';
import { tasksKeys } from './api';

export { ApiError as TaskApiError };

export type StepGranularity = 'coarse' | 'standard' | 'fine';

export interface ProposeStepsInput {
  taskId: string;
  granularity?: StepGranularity;
}

export interface StepProposal {
  title: string;
  description: string;
  priority: string;
}

/**
 * StepProposalUI — client-side wrapper around StepProposal that adds a
 * stable `uiId` for use as a React `key`. The id is generated locally
 * after each `/propose-steps` call and is NEVER sent back to the
 * server; it exists purely so list rows survive edits / reorders /
 * filters without React unmounting them and discarding their local
 * input state.
 */
export interface StepProposalUI extends StepProposal {
  uiId: string;
}

export interface ProposeStepsResult {
  parentTaskId: string;
  steps: StepProposalUI[];
}

/**
 * generateUiId — produces a UI-only stable identifier for a proposed
 * step. Prefers `crypto.randomUUID()` (available in browsers and in
 * the happy-dom test environment); falls back to a monotonic counter
 * combined with a timestamp on platforms where it is missing.
 */
let uiIdCounter = 0;
function generateUiId(): string {
  const c: Crypto | undefined =
    typeof globalThis !== 'undefined' ? (globalThis.crypto as Crypto | undefined) : undefined;
  if (c && typeof c.randomUUID === 'function') {
    return c.randomUUID();
  }
  uiIdCounter += 1;
  return `step-ui-${String(Date.now())}-${String(uiIdCounter)}`;
}

/**
 * useProposeSteps — POST /tasks/{id}/propose-steps.
 *
 * Calls the AI-backed endpoint to decompose a task into subtask steps.
 * Each returned step is augmented with a fresh `uiId` so callers can
 * use it as a stable React key. Re-proposing yields a new batch of
 * ids — they will not collide with previous proposals.
 */
export function useProposeSteps() {
  return useMutation<ProposeStepsResult, ApiError, ProposeStepsInput>({
    mutationFn: async ({ taskId, granularity }) => {
      const { data, error } = await sdk.POST('/tasks/{id}/propose-steps', {
        params: { path: { id: taskId } },
        body: { granularity: granularity ?? 'standard' },
      });
      if (error || !data) {
        const err = error as { detail?: string; title?: string; type?: string } | undefined;
        throw new ApiError(err?.type, err?.detail ?? err?.title ?? 'Failed to propose steps');
      }
      const raw = data as { parentTaskId: string; steps: StepProposal[] };
      return {
        parentTaskId: raw.parentTaskId,
        steps: raw.steps.map((step) => ({ ...step, uiId: generateUiId() })),
      };
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
  return useMutation<ApplyStepsResult, ApiError, ApplyStepsArgs>({
    mutationFn: async (args) => {
      const { data, error } = await sdk.POST('/tasks/{id}/apply-steps', {
        params: { path: { id: args.taskId } },
        body: { steps: args.steps },
      });
      if (error || !data) {
        const err = error as { detail?: string; title?: string; type?: string } | undefined;
        throw new ApiError(err?.type, err?.detail ?? err?.title ?? 'Failed to apply steps');
      }
      return data as ApplyStepsResult;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
    },
  });
}
