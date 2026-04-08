/**
 * Tasks feature — typed queries and mutations backed by the SDK.
 *
 * All hooks are suspense-ready where applicable and participate in the
 * shared QueryClient (throwOnError, route-level ErrorBoundary).
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type Task = components['schemas']['Task'];
export type TaskListItem = components['schemas']['TaskListItem'];
export type CreateTaskInput = components['schemas']['CreateTaskBody'];
export type PatchTaskInput = components['schemas']['PatchTaskBody'];

/** Backend `derived_state` enum (see sql/tables/tasks.sql). */
export type TaskDerivedState = 'open' | 'waiting' | 'review' | 'done' | 'cancelled';

/** Ordered list of all task states for column rendering. */
export const TASK_STATES: readonly TaskDerivedState[] = [
  'open',
  'waiting',
  'review',
  'done',
  'cancelled',
] as const;

/** Backend priority is int32 0..4. */
export type TaskPriority = 0 | 1 | 2 | 3 | 4;

export interface TaskFilters {
  search?: string;
  states?: readonly TaskDerivedState[];
  assigneeId?: string;
}

/** Query key factory for the tasks feature. */
export const tasksKeys = {
  all: ['tasks'] as const,
  list: (projectId: string, filters?: TaskFilters) =>
    [...tasksKeys.all, 'list', projectId, filters ?? {}] as const,
  detail: (id: string) => [...tasksKeys.all, 'detail', id] as const,
};

/** Lightweight error thrown when the SDK returns an error envelope. */
export class TaskApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'TaskApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): TaskApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new TaskApiError(code, message);
  }
  return new TaskApiError(undefined, fallback);
}

function applyFilters(items: TaskListItem[], filters: TaskFilters | undefined): TaskListItem[] {
  if (!filters) return items;
  const search = filters.search?.trim().toLowerCase() ?? '';
  const states = filters.states && filters.states.length > 0 ? new Set(filters.states) : null;
  return items.filter((t) => {
    if (search && !t.title.toLowerCase().includes(search)) return false;
    if (states && !states.has(t.derivedState as TaskDerivedState)) return false;
    // assigneeId is currently not exposed on TaskListItem; F8 will plumb actors
    // through the list response. Until then, this filter is a no-op client-side.
    return true;
  });
}

export function useTasksQuery(
  projectId: string,
  filters?: TaskFilters,
): UseSuspenseQueryResult<TaskListItem[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.list(projectId, filters),
    queryFn: async (): Promise<TaskListItem[]> => {
      const { data, error } = await sdk.GET('/tasks', {
        params: { query: { projectId, limit: 200, offset: 0 } },
      });
      if (error || !data) throw toError(error, 'Failed to load tasks');
      return applyFilters(data.tasks ?? [], filters);
    },
  });
}

export function useTaskQuery(taskId: string): UseSuspenseQueryResult<Task> {
  return useSuspenseQuery({
    queryKey: tasksKeys.detail(taskId),
    queryFn: async (): Promise<Task> => {
      const { data, error } = await sdk.GET('/tasks/{id}', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toError(error, 'Failed to load task');
      return data;
    },
  });
}

export function useCreateTask(): UseMutationResult<Task, TaskApiError, CreateTaskInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateTaskInput): Promise<Task> => {
      const { data, error } = await sdk.POST('/tasks', { body: input });
      if (error || !data) throw toError(error, 'Failed to create task');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list', vars.projectId] });
    },
  });
}

export interface UpdateTaskArgs {
  id: string;
  patch: PatchTaskInput;
}

export function useUpdateTask(): UseMutationResult<Task, TaskApiError, UpdateTaskArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, patch }: UpdateTaskArgs): Promise<Task> => {
      const { data, error } = await sdk.PATCH('/tasks/{id}', {
        params: { path: { id } },
        body: patch,
      });
      if (error || !data) throw toError(error, 'Failed to update task');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.id) });
      void qc.invalidateQueries({ queryKey: tasksKeys.all });
    },
  });
}

export function useDeleteTask(): UseMutationResult<void, TaskApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      const { error } = await sdk.DELETE('/tasks/{id}', {
        params: { path: { id } },
      });
      if (error) throw toError(error, 'Failed to delete task');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: tasksKeys.all });
    },
  });
}

export interface MoveTaskArgs {
  id: string;
  toState: TaskDerivedState;
}

/**
 * useMoveTask — board drag-and-drop handler.
 *
 * KNOWN LIMITATION (F6): the backend deliberately does not accept
 * `derivedState` writes via PATCH /tasks. State transitions happen via the
 * constraint engine + event bus. Until F7 introduces a `task.transition`
 * event endpoint, this mutation only invalidates the local cache so the
 * board UI snaps the card back. The mutation always rejects with a
 * domain error so callers can show a toast.
 */
export function useMoveTask(): UseMutationResult<void, TaskApiError, MoveTaskArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (_args: MoveTaskArgs): Promise<void> => {
      throw new TaskApiError(
        'WS_TASK_TRANSITION_NOT_IMPLEMENTED',
        'Task state transitions are not yet wired to the event bus.',
      );
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: tasksKeys.all });
    },
  });
}
