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
      void qc.invalidateQueries({ queryKey: ['tasks', 'detail', taskId] });
    },
  });
}

export interface CompileConstraintArgs {
  taskId: string;
  prompt: string;
}

export interface CompileConstraintResult {
  kind: string;
  expression: string;
}

/**
 * useCompileConstraint — mutation that sends a natural-language prompt
 * to the AI compile endpoint and returns the generated DSL kind + expression.
 */
export function useCompileConstraint(): UseMutationResult<
  CompileConstraintResult,
  Error,
  CompileConstraintArgs
> {
  return useMutation<CompileConstraintResult, Error, CompileConstraintArgs>({
    mutationFn: async ({ taskId, prompt }): Promise<CompileConstraintResult> => {
      const { data, error } = await sdk.POST('/tasks/{id}/constraints/compile', {
        params: { path: { id: taskId } },
        body: { prompt },
      });
      if (error || !data) throw new Error('Failed to compile constraint');
      return { kind: data.kind, expression: data.expression };
    },
  });
}

export interface ExplainConstraintArgs {
  taskId: string;
  expression: string;
}

export interface ExplainConstraintResult {
  explanation: string;
}

/**
 * useExplainConstraint — mutation that sends a DSL expression to the
 * explain endpoint and returns a human-readable explanation.
 */
export function useExplainConstraint(): UseMutationResult<
  ExplainConstraintResult,
  Error,
  ExplainConstraintArgs
> {
  return useMutation<ExplainConstraintResult, Error, ExplainConstraintArgs>({
    mutationFn: async ({ taskId, expression }): Promise<ExplainConstraintResult> => {
      const { data, error } = await sdk.POST('/tasks/{id}/constraints/explain', {
        params: { path: { id: taskId } },
        body: { expression },
      });
      if (error || !data) throw new Error('Failed to explain constraint');
      return { explanation: data.explanation };
    },
  });
}

/**
 * useRemoveConstraint — manual intervention (4.WEB-3): drop a
 * constraint that the operator deems no longer relevant. This is the
 * "force satisfy" escape hatch — removing the row makes the engine
 * stop blocking on it.
 */
export interface RemoveConstraintArgs {
  taskId: string;
  constraintId: string;
}

export function useRemoveConstraint(): UseMutationResult<
  { ok: true },
  Error,
  RemoveConstraintArgs
> {
  const qc = useQueryClient();
  return useMutation<{ ok: true }, Error, RemoveConstraintArgs>({
    mutationFn: async ({ taskId, constraintId }) => {
      const { error } = await sdk.DELETE('/tasks/{id}/constraints/{cid}', {
        params: { path: { id: taskId, cid: constraintId } },
      });
      if (error) throw new Error('Failed to remove constraint');
      return { ok: true };
    },
    onSuccess: (_res, { taskId }) => {
      void qc.invalidateQueries({ queryKey: ['tasks', 'detail', taskId] });
    },
  });
}
