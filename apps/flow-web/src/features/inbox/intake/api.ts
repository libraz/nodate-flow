/**
 * Intake feature — typed react-query hooks for the workspace-scoped
 * triage queue (`/workspaces/{wsId}/intake`). Intake items are inbound
 * candidates that haven't been promoted to tasks yet; this module
 * surfaces:
 *
 *   - {@link useIntakeQuery}                  GET    /workspaces/{wsId}/intake
 *   - {@link useCreateIntakeItemMutation}     POST   /workspaces/{wsId}/intake
 *   - {@link useTriageIntakeItemMutation}     PATCH  /workspaces/{wsId}/intake/{id}
 *   - {@link useConvertIntakeItemMutation}    POST   /workspaces/{wsId}/intake/{id}/convert
 *
 * Mutations invalidate the workspace's intake list. Convert also kicks
 * the per-workspace task list cache so the freshly created task appears
 * immediately in `/tasks`.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../../lib/api-error';
import { sdk } from '../../../lib/sdk';

/** Intake item DTO mirrored from the generated SDK. */
export type IntakeItem = components['schemas']['IntakeItem'];

/**
 * Triage status filter. Mirrors the backend enum so the picker stays in
 * sync with what the API understands. The UI usually pins this to
 * `pending` (default queue) but the backend supports each variant for
 * future filter chips.
 */
export type IntakeStatus = 'pending' | 'accepted' | 'rejected' | 'snoozed' | 'duplicate';

export const intakeKeys = {
  all: ['intake'] as const,
  list: (wsId: string, status: IntakeStatus) => ['intake', wsId, status] as const,
};

/**
 * GET /workspaces/{wsId}/intake — list intake items for the workspace,
 * filtered by triage status. Suspense-backed; the consumer renders a
 * fallback while the first load resolves.
 */
export function useIntakeQuery(
  wsId: string,
  status: IntakeStatus = 'pending',
): UseSuspenseQueryResult<IntakeItem[]> {
  return useSuspenseQuery({
    queryKey: intakeKeys.list(wsId, status),
    queryFn: async (): Promise<IntakeItem[]> => {
      if (!wsId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/intake', {
        params: { path: { wsId }, query: { status, limit: 200, offset: 0 } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load intake items');
      return data.items ?? [];
    },
  });
}

export interface CreateIntakeItemArgs {
  wsId: string;
  title: string;
  body?: string;
}

/**
 * POST /workspaces/{wsId}/intake — quick-add a new intake item from the
 * tab's compose row. Optimistic insert is intentionally skipped: the
 * backend assigns ai-score asynchronously, so we wait for the response
 * before mutating the cache to keep the row representation truthful.
 */
export function useCreateIntakeItemMutation(): UseMutationResult<
  IntakeItem,
  ApiError,
  CreateIntakeItemArgs
> {
  const qc = useQueryClient();
  return useMutation<IntakeItem, ApiError, CreateIntakeItemArgs>({
    mutationFn: async ({ wsId, title, body }): Promise<IntakeItem> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/intake', {
        params: { path: { wsId } },
        body: { title, ...(body ? { body } : {}) },
      });
      if (error || !data) throw toApiError(error, 'Failed to create intake item');
      return data;
    },
    onSuccess: (_data, { wsId }) => {
      void qc.invalidateQueries({ queryKey: ['intake', wsId] });
    },
  });
}

export interface TriageIntakeItemArgs {
  wsId: string;
  id: string;
  status: 'accepted' | 'rejected' | 'snoozed' | 'duplicate';
  snoozeUntil?: number;
}

/**
 * PATCH /workspaces/{wsId}/intake/{id} — change an intake item's triage
 * status. Used by the dismiss/snooze affordances on each row.
 */
export function useTriageIntakeItemMutation(): UseMutationResult<
  IntakeItem,
  ApiError,
  TriageIntakeItemArgs
> {
  const qc = useQueryClient();
  return useMutation<IntakeItem, ApiError, TriageIntakeItemArgs>({
    mutationFn: async ({ wsId, id, status, snoozeUntil }): Promise<IntakeItem> => {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}/intake/{id}', {
        params: { path: { wsId, id } },
        body: { status, ...(snoozeUntil ? { snoozeUntil } : {}) },
      });
      if (error || !data) throw toApiError(error, 'Failed to update intake item');
      return data;
    },
    onSuccess: (_data, { wsId }) => {
      void qc.invalidateQueries({ queryKey: ['intake', wsId] });
    },
  });
}

export interface ConvertIntakeItemArgs {
  wsId: string;
  id: string;
  projectId: string;
}

/**
 * POST /workspaces/{wsId}/intake/{id}/convert — promote an intake item
 * to a task in the chosen project. The backend marks the source row
 * `accepted` and stores the new task's id, so we invalidate both the
 * intake list and the workspace's task list cache.
 */
export function useConvertIntakeItemMutation(): UseMutationResult<
  { taskId: string },
  ApiError,
  ConvertIntakeItemArgs
> {
  const qc = useQueryClient();
  return useMutation<{ taskId: string }, ApiError, ConvertIntakeItemArgs>({
    mutationFn: async ({ wsId, id, projectId }): Promise<{ taskId: string }> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/intake/{id}/convert', {
        params: { path: { wsId, id } },
        body: { projectId },
      });
      if (error || !data) throw toApiError(error, 'Failed to convert intake item');
      return { taskId: data.taskId };
    },
    onSuccess: (_data, { wsId }) => {
      void qc.invalidateQueries({ queryKey: ['intake', wsId] });
      void qc.invalidateQueries({ queryKey: ['tasks'] });
    },
  });
}
