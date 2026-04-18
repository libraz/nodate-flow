/**
 * Auto-action rules — typed queries and mutations backed by the SDK.
 *
 * Each workspace has a fixed set of rule kinds. The API returns all rules
 * on GET and accepts a sparse PATCH (only changed rules need to be sent).
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

/** The four auto-action rule kinds. */
export type AutoActionRuleKind =
  | 'escalate_overdue'
  | 'assign_owner'
  | 'nudge_assignee'
  | 'close_stale_review';

/** A single auto-action rule as returned by the API. */
export interface AutoActionRule {
  kind: AutoActionRuleKind;
  enabled: boolean;
  confidence: number;
  idleHours: number;
}

/** Sparse patch for a single rule — only changed fields are required. */
export interface PatchAutoActionRule {
  kind: AutoActionRuleKind;
  enabled?: boolean;
  confidence?: number;
  idleHours?: number;
}

/** Query key factory for auto-action rules. */
export const autoActionRulesKeys = {
  all: ['workspaces'] as const,
  rules: (wsId: string) => ['workspaces', wsId, 'auto-action-rules'] as const,
};

/** Suspense query for the per-rule configuration. */
export function useAutoActionRulesQuery(
  workspaceId: string,
): UseSuspenseQueryResult<AutoActionRule[]> {
  return useSuspenseQuery({
    queryKey: autoActionRulesKeys.rules(workspaceId),
    queryFn: async (): Promise<AutoActionRule[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/auto-action-rules', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) {
        throw new Error('Failed to load auto-action rules');
      }
      // The API wraps the array in { rules: [...] }.
      return (data as { rules: AutoActionRule[] }).rules;
    },
  });
}

export interface UpdateAutoActionRulesArgs {
  workspaceId: string;
  rules: PatchAutoActionRule[];
}

/** Mutation to PATCH auto-action rules. */
export function useUpdateAutoActionRules(): UseMutationResult<
  AutoActionRule[],
  Error,
  UpdateAutoActionRulesArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      workspaceId,
      rules,
    }: UpdateAutoActionRulesArgs): Promise<AutoActionRule[]> => {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}/ai/auto-action-rules', {
        params: { path: { wsId: workspaceId } },
        body: { rules },
      });
      if (error || !data) {
        throw new Error('Failed to update auto-action rules');
      }
      return (data as { rules: AutoActionRule[] }).rules;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: autoActionRulesKeys.rules(vars.workspaceId),
      });
    },
  });
}
