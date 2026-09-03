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

import { apiRequest } from '../../lib/api';

/** The four auto-action rule kinds. */
export type AutoActionRuleKind =
  | 'escalate_overdue'
  | 'assign_owner'
  | 'nudge_assignee'
  | 'close_stale_review';

/**
 * Operator-picked autonomy override.
 *
 * When set on a rule row, the resolver returns this level verbatim and
 * skips the confidence gate. When undefined/null, the row falls back to
 * confidence-based derivation (or the YAML default for the signal kind).
 */
export type AutonomyLevel = 'suggest' | 'draft' | 'auto';

/** A single auto-action rule as returned by the API. */
export interface AutoActionRule {
  kind: AutoActionRuleKind;
  enabled: boolean;
  confidence: number;
  idleHours: number;
  /** Dotted signal-kind identifier (e.g. `discord.presence`). Omitted means wildcard. */
  signalKind?: string;
  /** Operator-picked autonomy override; absent means "unset" (use defaults). */
  autonomyLevel?: AutonomyLevel;
}

/** Sparse patch for a single rule — only changed fields are required. */
export interface PatchAutoActionRule {
  kind: AutoActionRuleKind;
  enabled?: boolean;
  confidence?: number;
  idleHours?: number;
  /** Dotted signal-kind identifier scope for this patch entry. */
  signalKind?: string;
  /** New autonomy level; omitting preserves the prior value. */
  autonomyLevel?: AutonomyLevel;
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
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/auto-action-rules', {
            params: { path: { wsId: workspaceId } },
          }),
        'Failed to load auto-action rules',
      );
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
      const data = await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/auto-action-rules', {
            params: { path: { wsId: workspaceId } },
            body: { rules },
          }),
        'Failed to update auto-action rules',
      );
      return (data.rules ?? []) as AutoActionRule[];
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: autoActionRulesKeys.rules(vars.workspaceId),
      });
    },
  });
}
