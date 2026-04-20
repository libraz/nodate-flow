/**
 * Intake feature — typed queries and mutations.
 */
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

// TODO: Replace with SDK types after `bun run generate:sdk`
export interface IntakeItem {
  id: string;
  title: string;
  body?: string;
  triageStatus: string;
  snoozeUntil?: number;
  aiScore?: number;
  aiReasoning?: string;
  taskId?: string;
  triagedByUserId?: string;
  triagedByDisplayName?: string;
  createdAt: number;
}

/** Query key factory for the intake feature. */
export const intakeKeys = {
  all: ['intake'] as const,
  list: (workspaceId: string, status?: string) =>
    [...intakeKeys.all, 'list', workspaceId, status ?? ''] as const,
};

export function useIntakeQuery(
  workspaceId: string,
  status?: string,
): UseSuspenseQueryResult<IntakeItem[]> {
  return useSuspenseQuery({
    queryKey: intakeKeys.list(workspaceId, status),
    queryFn: async (): Promise<IntakeItem[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/intake', {
        params: {
          path: { wsId: workspaceId },
          query: { ...(status != null ? { status } : {}), limit: 100, offset: 0 },
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to load intake items');
      return (data as { items?: IntakeItem[] }).items ?? [];
    },
  });
}

export interface CreateIntakeArgs {
  workspaceId: string;
  title: string;
  body?: string;
}

export function useCreateIntake(): UseMutationResult<IntakeItem, ApiError, CreateIntakeArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, title, body }: CreateIntakeArgs): Promise<IntakeItem> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/intake', {
        params: { path: { wsId: workspaceId } },
        body: { title, ...(body != null ? { body } : {}) },
      });
      if (error || !data) throw toApiError(error, 'Failed to create intake item');
      return data as IntakeItem;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: intakeKeys.list(vars.workspaceId) });
    },
  });
}

export interface TriageIntakeArgs {
  workspaceId: string;
  id: string;
  status: 'accepted' | 'rejected' | 'snoozed' | 'duplicate';
  snoozeUntil?: number;
}

export function useTriageIntake(): UseMutationResult<void, ApiError, TriageIntakeArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      workspaceId,
      id,
      status,
      snoozeUntil,
    }: TriageIntakeArgs): Promise<void> => {
      const { error } = await sdk.PATCH('/workspaces/{wsId}/intake/{id}', {
        params: { path: { wsId: workspaceId, id } },
        body: { status, ...(snoozeUntil != null ? { snoozeUntil } : {}) },
      });
      if (error) throw toApiError(error, 'Failed to triage item');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: intakeKeys.list(vars.workspaceId) });
    },
  });
}

export interface ConvertIntakeArgs {
  workspaceId: string;
  id: string;
  projectId: string;
}

export function useConvertIntake(): UseMutationResult<
  { taskId: string },
  ApiError,
  ConvertIntakeArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      workspaceId,
      id,
      projectId,
    }: ConvertIntakeArgs): Promise<{ taskId: string }> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/intake/{id}/convert', {
        params: { path: { wsId: workspaceId, id } },
        body: { projectId },
      });
      if (error || !data) throw toApiError(error, 'Failed to convert intake item');
      return data as { taskId: string };
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: intakeKeys.list(vars.workspaceId) });
      void qc.invalidateQueries({ queryKey: ['tasks', 'list'] });
    },
  });
}
