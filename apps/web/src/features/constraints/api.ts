/**
 * Constraints feature — Phase 3 constraint engine hooks.
 *
 * - useEvaluateConstraints: POST /tasks/{id}/constraints/evaluate
 *   (3.WEB-1 constraint editor — "evaluate now" button)
 * - useAddConstraint: POST /tasks/{id}/constraints
 *   (3.WEB-1 constraint editor — "save" action)
 */

import type { components } from '@nodate-flow/sdk';
import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type ConstraintOutcome = components['schemas']['EvaluateConstraintsOutcome'];

export interface EvaluateArgs {
  taskId: string;
}

export interface EvaluateResult {
  outcomes: ConstraintOutcome[];
}

/**
 * useEvaluateConstraints — mutation that runs the constraint engine
 * for a single task and returns per-constraint outcomes.
 */
export function useEvaluateConstraints(): UseMutationResult<EvaluateResult, Error, EvaluateArgs> {
  return useMutation<EvaluateResult, Error, EvaluateArgs>({
    mutationFn: async ({ taskId }): Promise<EvaluateResult> => {
      const { data, error } = await sdk.POST('/tasks/{id}/constraints/evaluate', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw new Error('Failed to evaluate constraints');
      return { outcomes: data.outcomes ?? [] };
    },
  });
}

export type ConstraintKind = 'deadline' | 'dependency' | 'approval' | 'signal' | 'custom';

export interface AddConstraintArgs {
  taskId: string;
  kind: ConstraintKind;
  expression: string;
}

/**
 * useAddConstraint — mutation for POST /tasks/{id}/constraints.
 * Invalidates the task query on success so the board reflects the
 * new constraint row.
 */
export function useAddConstraint(): UseMutationResult<{ id: string }, Error, AddConstraintArgs> {
  const qc = useQueryClient();
  return useMutation<{ id: string }, Error, AddConstraintArgs>({
    mutationFn: async ({ taskId, kind, expression }) => {
      const { data, error } = await sdk.POST('/tasks/{id}/constraints', {
        params: { path: { id: taskId } },
        body: { kind, expression },
      });
      if (error || !data) throw new Error('Failed to add constraint');
      return { id: data.id };
    },
    onSuccess: (_res, { taskId }) => {
      qc.invalidateQueries({ queryKey: ['tasks', taskId] });
    },
  });
}
