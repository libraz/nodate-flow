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
export type TaskComment = components['schemas']['TaskComment'];
export type TaskActor = components['schemas']['TaskActor'];
export type CreateTaskInput = components['schemas']['CreateTaskBody'];
export type PatchTaskInput = components['schemas']['PatchTaskBody'];
export type TransitionInput = components['schemas']['TransitionTaskBody'];
export type TransitionName = TransitionInput['transition'];
export type AddCommentInput = components['schemas']['AddTaskCommentBody'];
export type AddActorInput = components['schemas']['AddTaskActorBody'];

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
  comments: (id: string) => [...tasksKeys.all, 'detail', id, 'comments'] as const,
  actors: (id: string) => [...tasksKeys.all, 'detail', id, 'actors'] as const,
  duplicates: (id: string) => [...tasksKeys.all, 'detail', id, 'duplicates'] as const,
  inferState: (id: string) => [...tasksKeys.all, 'detail', id, 'infer-state'] as const,
};

export type InferStateProposal = components['schemas']['InferStateProposal'];
export interface InferStateResult {
  taskId: string;
  state: TaskDerivedState;
  proposal?: InferStateProposal;
}

export type DuplicateCandidate = components['schemas']['DuplicateCandidate'];
export interface DuplicatesResult {
  model: string;
  candidates: DuplicateCandidate[];
}

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

/**
 * Legal transitions from a given derived state, per ADR 0001 (v1 state
 * machine) as specialized for the board drop / transitions panel UX.
 *
 * Keep this in sync with `transitionForDrop` below — the transitions panel
 * simply enumerates these, while the board infers the right transition from
 * the (from, to) column pair.
 */
export const TRANSITIONS_BY_STATE: Record<TaskDerivedState, readonly TransitionName[]> = {
  open: ['start', 'cancel'],
  waiting: ['submit', 'block', 'complete', 'cancel'],
  review: ['complete', 'cancel'],
  done: ['reopen', 'cancel'],
  cancelled: ['reopen'],
};

/**
 * Map a board column drop onto a state machine transition, or `null` if the
 * drop is illegal. See task spec F7.
 */
export function transitionForDrop(
  from: TaskDerivedState,
  to: TaskDerivedState,
): TransitionName | null {
  if (from === to) return null;
  switch (to) {
    case 'waiting':
      if (from === 'open') return 'start';
      if (from === 'review') return 'unblock';
      if (from === 'done' || from === 'cancelled') return 'reopen';
      return null;
    case 'review':
      if (from === 'waiting') return 'submit';
      return null;
    case 'done':
      if (from === 'review' || from === 'waiting') return 'complete';
      return null;
    case 'open':
      if (from === 'cancelled') return 'reopen';
      if (from === 'waiting') return 'block';
      return null;
    case 'cancelled':
      return 'cancel';
    default:
      return null;
  }
}

export function useTasksQuery(
  projectId: string,
  filters?: TaskFilters,
): UseSuspenseQueryResult<TaskListItem[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.list(projectId, filters),
    queryFn: async (): Promise<TaskListItem[]> => {
      const search = filters?.search?.trim() ?? '';
      const states = filters?.states ?? [];
      const assignee = filters?.assigneeId?.trim() ?? '';
      const query: {
        projectId: string;
        limit: number;
        offset: number;
        q?: string;
        state?: string[];
        assignee?: string;
      } = {
        projectId,
        limit: 200,
        offset: 0,
      };
      if (search.length > 0) query.q = search;
      if (states.length > 0) query.state = [...states];
      if (assignee.length > 0) query.assignee = assignee;
      const { data, error } = await sdk.GET('/tasks', { params: { query } });
      if (error || !data) throw toError(error, 'Failed to load tasks');
      return data.tasks ?? [];
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

export function useTaskDuplicatesQuery(taskId: string): UseSuspenseQueryResult<DuplicatesResult> {
  return useSuspenseQuery({
    queryKey: tasksKeys.duplicates(taskId),
    queryFn: async (): Promise<DuplicatesResult> => {
      const { data, error } = await sdk.GET('/tasks/{id}/duplicates', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toError(error, 'Failed to load duplicates');
      return { model: data.model, candidates: data.candidates ?? [] };
    },
  });
}

export function useTaskInferStateQuery(taskId: string): UseSuspenseQueryResult<InferStateResult> {
  return useSuspenseQuery({
    queryKey: tasksKeys.inferState(taskId),
    queryFn: async (): Promise<InferStateResult> => {
      const { data, error } = await sdk.GET('/tasks/{id}/infer-state', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toError(error, 'Failed to infer state');
      const result: InferStateResult = {
        taskId: data.taskId,
        state: data.state as TaskDerivedState,
      };
      if (data.proposal) result.proposal = data.proposal;
      return result;
    },
  });
}

export function useTaskActorsQuery(taskId: string): UseSuspenseQueryResult<TaskActor[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.actors(taskId),
    queryFn: async (): Promise<TaskActor[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/actors', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toError(error, 'Failed to load task actors');
      return data.actors ?? [];
    },
  });
}

export interface AddActorArgs {
  taskId: string;
  input: AddActorInput;
}

export function useAddTaskActor(): UseMutationResult<TaskActor, TaskApiError, AddActorArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, input }: AddActorArgs): Promise<TaskActor> => {
      const { data, error } = await sdk.POST('/tasks/{id}/actors', {
        params: { path: { id: taskId } },
        body: input,
      });
      if (error || !data) throw toError(error, 'Failed to add task actor');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.actors(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
    },
  });
}

export interface RemoveActorArgs {
  taskId: string;
  actorId: string;
}

export function useRemoveTaskActor(): UseMutationResult<void, TaskApiError, RemoveActorArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, actorId }: RemoveActorArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/tasks/{id}/actors/{actorId}', {
        params: { path: { id: taskId, actorId } },
      });
      if (error) throw toError(error, 'Failed to remove task actor');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.actors(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
    },
  });
}

export function useTaskCommentsQuery(taskId: string): UseSuspenseQueryResult<TaskComment[]> {
  return useSuspenseQuery({
    queryKey: tasksKeys.comments(taskId),
    queryFn: async (): Promise<TaskComment[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/comments', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toError(error, 'Failed to load comments');
      return data.comments ?? [];
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

export interface AddCommentArgs {
  taskId: string;
  body: string;
}

export function useAddTaskComment(): UseMutationResult<TaskComment, TaskApiError, AddCommentArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, body }: AddCommentArgs): Promise<TaskComment> => {
      const { data, error } = await sdk.POST('/tasks/{id}/comments', {
        params: { path: { id: taskId } },
        body: { body },
      });
      if (error || !data) throw toError(error, 'Failed to add comment');
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.comments(vars.taskId) });
    },
  });
}

export interface TransitionTaskArgs {
  id: string;
  transition: TransitionName;
  /** Optional projectId for cache list invalidation. */
  projectId?: string;
  /** Optional expected target state for optimistic list update. */
  optimisticState?: TaskDerivedState;
}

/**
 * useTransitionTask — POST /tasks/{id}/transitions.
 *
 * Performs optimistic cache updates for `tasks.list` (move the card between
 * columns) and `tasks.detail` (flip derivedState), then reconciles with the
 * authoritative Task returned by the server. Rolls back on error.
 */
export function useTransitionTask(): UseMutationResult<Task, TaskApiError, TransitionTaskArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, transition }: TransitionTaskArgs): Promise<Task> => {
      const { data, error } = await sdk.POST('/tasks/{id}/transitions', {
        params: { path: { id } },
        body: { transition },
      });
      if (error || !data) throw toError(error, 'Failed to apply transition');
      return data;
    },
    onMutate: async (vars) => {
      await qc.cancelQueries({ queryKey: tasksKeys.all });
      const listSnapshots = qc.getQueriesData<TaskListItem[]>({
        queryKey: [...tasksKeys.all, 'list'],
      });
      const detailSnapshot = qc.getQueryData<Task>(tasksKeys.detail(vars.id));
      if (vars.optimisticState) {
        const nextState = vars.optimisticState;
        for (const [key, value] of listSnapshots) {
          if (!value) continue;
          const updated = value.map((task) =>
            task.id === vars.id ? { ...task, derivedState: nextState } : task,
          );
          qc.setQueryData(key, updated);
        }
        if (detailSnapshot) {
          qc.setQueryData(tasksKeys.detail(vars.id), {
            ...detailSnapshot,
            derivedState: nextState,
          });
        }
      }
      return { listSnapshots, detailSnapshot };
    },
    onError: (_err, vars, ctx) => {
      const snap = ctx as
        | {
            listSnapshots: [readonly unknown[], TaskListItem[] | undefined][];
            detailSnapshot: Task | undefined;
          }
        | undefined;
      if (!snap) return;
      for (const [key, value] of snap.listSnapshots) {
        qc.setQueryData(key, value);
      }
      if (snap.detailSnapshot) {
        qc.setQueryData(tasksKeys.detail(vars.id), snap.detailSnapshot);
      }
    },
    onSuccess: (task) => {
      qc.setQueryData(tasksKeys.detail(task.id), task);
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.id) });
      if (vars.projectId) {
        void qc.invalidateQueries({
          queryKey: [...tasksKeys.all, 'list', vars.projectId],
        });
      } else {
        void qc.invalidateQueries({ queryKey: [...tasksKeys.all, 'list'] });
      }
    },
  });
}
