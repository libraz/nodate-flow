/**
 * Labels feature — typed queries and mutations backed by the SDK.
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

export interface Label {
  id: string;
  name: string;
  color: string;
  description?: string;
  sortWeight?: number;
  createdAt?: number;
}

/** Query key factory for the labels feature. */
export const labelsKeys = {
  all: ['labels'] as const,
  list: (workspaceId: string) => [...labelsKeys.all, 'list', workspaceId] as const,
  forTask: (taskId: string) => [...labelsKeys.all, 'forTask', taskId] as const,
};

export function useLabelsQuery(workspaceId: string): UseSuspenseQueryResult<Label[]> {
  return useSuspenseQuery({
    queryKey: labelsKeys.list(workspaceId),
    queryFn: async (): Promise<Label[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/labels', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load labels');
      return (data.labels ?? []) as Label[];
    },
  });
}

export function useTaskLabelsQuery(taskId: string): UseSuspenseQueryResult<Label[]> {
  return useSuspenseQuery({
    queryKey: labelsKeys.forTask(taskId),
    queryFn: async (): Promise<Label[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/labels', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load task labels');
      return (data.labels ?? []) as Label[];
    },
  });
}

export interface CreateLabelArgs {
  workspaceId: string;
  input: { name: string; color: string; description?: string };
}

export function useCreateLabel(): UseMutationResult<Label, ApiError, CreateLabelArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, input }: CreateLabelArgs): Promise<Label> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/labels', {
        params: { path: { wsId: workspaceId } },
        body: input,
      });
      if (error || !data) throw toApiError(error, 'Failed to create label');
      return data as Label;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: labelsKeys.list(vars.workspaceId) });
    },
  });
}

export interface UpdateLabelArgs {
  labelId: string;
  workspaceId: string;
  patch: { name?: string; color?: string; description?: string };
}

export function useUpdateLabel(): UseMutationResult<Label, ApiError, UpdateLabelArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ labelId, workspaceId, patch }: UpdateLabelArgs): Promise<Label> => {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}/labels/{id}', {
        params: { path: { wsId: workspaceId, id: labelId } },
        body: patch,
      });
      if (error || !data) throw toApiError(error, 'Failed to update label');
      return data as Label;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: labelsKeys.list(vars.workspaceId) });
    },
  });
}

export interface DeleteLabelArgs {
  labelId: string;
  workspaceId: string;
}

export function useDeleteLabel(): UseMutationResult<void, ApiError, DeleteLabelArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ labelId, workspaceId }: DeleteLabelArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/labels/{id}', {
        params: { path: { wsId: workspaceId, id: labelId } },
      });
      if (error) throw toApiError(error, 'Failed to delete label');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: labelsKeys.list(vars.workspaceId) });
    },
  });
}

export interface AddTaskLabelArgs {
  taskId: string;
  labelId: string;
}

export function useAddTaskLabel(): UseMutationResult<void, ApiError, AddTaskLabelArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, labelId }: AddTaskLabelArgs): Promise<void> => {
      const { error } = await sdk.POST('/tasks/{id}/labels', {
        params: { path: { id: taskId } },
        body: { labelId },
      });
      if (error) throw toApiError(error, 'Failed to add label to task');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: labelsKeys.forTask(vars.taskId) });
      void qc.invalidateQueries({ queryKey: ['tasks', 'detail', vars.taskId] });
      void qc.invalidateQueries({ queryKey: ['tasks', 'list'] });
    },
  });
}

export interface RemoveTaskLabelArgs {
  taskId: string;
  labelId: string;
}

export function useRemoveTaskLabel(): UseMutationResult<void, ApiError, RemoveTaskLabelArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, labelId }: RemoveTaskLabelArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/tasks/{id}/labels/{labelId}', {
        params: { path: { id: taskId, labelId } },
      });
      if (error) throw toApiError(error, 'Failed to remove label from task');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: labelsKeys.forTask(vars.taskId) });
      void qc.invalidateQueries({ queryKey: ['tasks', 'detail', vars.taskId] });
      void qc.invalidateQueries({ queryKey: ['tasks', 'list'] });
    },
  });
}
